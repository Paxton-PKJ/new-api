package middleware

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appI18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

const (
	tokenMappingClientModel = "claude-opus-4-8"
	tokenMappingLogical     = "dsv4f"
)

// newTokenModelMappingContext builds a request context with an optional token
// mapping, the only state ApplyTokenModelMapping depends on.
func newTokenModelMappingContext(method, target, body, contentType string, mapping map[string]string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}
	if mapping != nil {
		common.SetContextKey(c, constant.ContextKeyTokenModelMapping, mapping)
	}
	return c
}

// setupTokenMappingChannelSelectDB provisions the channel cache an auto-group
// distribution needs, on its own in-memory database.
func setupTokenMappingChannelSelectDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	previousRetryTimes := common.RetryTimes
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","group-a":"Group A","group-b":"Group B"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"group-a":1,"group-b":1}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("5"))

	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		common.RetryTimes = previousRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", previousMaxTokenAutoGroups)))

		if previousMemoryCacheEnabled && previousDB != nil &&
			previousDB.Migrator().HasTable(&model.Channel{}) && previousDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	return db
}

func createTokenMappingChannel(t *testing.T, db *gorm.DB, id int, group, modelName, modelMapping string) {
	t.Helper()
	priority := int64(0)
	weight := uint(100)
	channel := &model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Weight:   &weight,
		Models:   modelName,
		Group:    group,
		Priority: &priority,
	}
	if modelMapping != "" {
		channel.ModelMapping = common.GetPointer(modelMapping)
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

type tokenMappingDistributeSetup struct {
	mapping     map[string]string
	modelLimits bool
	limitModels map[string]bool
}

func newTokenMappingDistributeRouter(setup tokenMappingDistributeSetup, terminal gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/chat/completions", RequestId(), func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
		common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"group-a", "group-b"})
		common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, true)
		c.Set("token_model_limit_enabled", setup.modelLimits)
		if setup.limitModels != nil {
			c.Set("token_model_limit", setup.limitModels)
		}
		if setup.mapping != nil {
			common.SetContextKey(c, constant.ContextKeyTokenModelMapping, setup.mapping)
		}
		c.Set("id", 1)
	}, Distribute(), terminal)
	return router
}

func performTokenMappingRequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestApplyTokenModelMappingRewritesJSONBody(t *testing.T) {
	body := `{"model":"claude-opus-4-8","temperature":0.10000000000000001,"big":12345678901234567890,"messages":[{"role":"user","content":"hi"}],"extra":{"k":[1,2]}}`
	c := newTokenModelMappingContext(http.MethodPost, "/v1/chat/completions", body, "application/json",
		map[string]string{tokenMappingClientModel: tokenMappingLogical})
	previous, err := common.GetBodyStorage(c)
	require.NoError(t, err)

	modelRequest := &ModelRequest{Model: tokenMappingClientModel}
	require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

	assert.Equal(t, tokenMappingLogical, modelRequest.Model)

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	rewritten, err := storage.Bytes()
	require.NoError(t, err)
	assert.Equal(t, tokenMappingLogical, gjson.GetBytes(rewritten, "model").String())

	// Everything except the model field must survive byte for byte: number
	// precision, key order, and unknown fields included.
	before, err := sjson.DeleteBytes([]byte(body), "model")
	require.NoError(t, err)
	after, err := sjson.DeleteBytes(rewritten, "model")
	require.NoError(t, err)
	assert.Equal(t, before, after)

	assert.Equal(t, int64(len(rewritten)), c.Request.ContentLength)
	var decoded struct{ Model string }
	require.NoError(t, common.UnmarshalBodyReusable(c, &decoded))
	assert.Equal(t, tokenMappingLogical, decoded.Model)

	_, err = previous.Bytes()
	assert.ErrorIs(t, err, common.ErrStorageClosed)
	assert.Equal(t, tokenMappingClientModel, common.GetContextKeyString(c, constant.ContextKeyTokenModelMappingClientModel))
}

func TestApplyTokenModelMappingRewritesDiskStorage(t *testing.T) {
	previousConfig := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 64, Path: t.TempDir()})
	t.Cleanup(func() { common.SetDiskCacheConfig(previousConfig) })

	body := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`
	c := newTokenModelMappingContext(http.MethodPost, "/v1/chat/completions", body, "application/json",
		map[string]string{tokenMappingClientModel: tokenMappingLogical})
	// The replacement storage holds an open cache file; releasing it before
	// t.TempDir's RemoveAll lets the cleanup delete it on Windows.
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	previous, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	require.True(t, previous.IsDisk())

	modelRequest := &ModelRequest{Model: tokenMappingClientModel}
	require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	assert.True(t, storage.IsDisk())
	rewritten, err := storage.Bytes()
	require.NoError(t, err)
	assert.Equal(t, tokenMappingLogical, gjson.GetBytes(rewritten, "model").String())

	_, err = previous.Bytes()
	assert.ErrorIs(t, err, common.ErrStorageClosed)
	entries, err := os.ReadDir(common.GetDiskCacheDir())
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, tokenMappingLogical, modelRequest.Model)
}

func TestApplyTokenModelMappingRewritesGeminiPath(t *testing.T) {
	tests := []struct {
		name         string
		target       string
		targetModel  string
		wantPath     string
		wantRawQuery string
		wantApplied  bool
	}{
		{
			name:         "v1beta stream action keeps action and query",
			target:       "/v1beta/models/claude-opus-4-8:streamGenerateContent?alt=sse",
			targetModel:  tokenMappingLogical,
			wantPath:     "/v1beta/models/dsv4f:streamGenerateContent",
			wantRawQuery: "alt=sse",
			wantApplied:  true,
		},
		{
			name:        "v1 generate content action",
			target:      "/v1/models/claude-opus-4-8:generateContent",
			targetModel: tokenMappingLogical,
			wantPath:    "/v1/models/dsv4f:generateContent",
			wantApplied: true,
		},
		{
			name:        "target containing slash is not applied",
			target:      "/v1beta/models/claude-opus-4-8:generateContent",
			targetModel: "a/b",
			wantPath:    "/v1beta/models/claude-opus-4-8:generateContent",
		},
		{
			name:        "target containing colon is not applied",
			target:      "/v1beta/models/claude-opus-4-8:generateContent",
			targetModel: "x:y",
			wantPath:    "/v1beta/models/claude-opus-4-8:generateContent",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c := newTokenModelMappingContext(http.MethodPost, testCase.target, `{}`, "application/json",
				map[string]string{tokenMappingClientModel: testCase.targetModel})
			originalRequestURI := c.Request.RequestURI

			modelRequest := &ModelRequest{Model: tokenMappingClientModel}
			require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

			assert.Equal(t, testCase.wantPath, c.Request.URL.Path)
			_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
			if testCase.wantApplied {
				assert.Equal(t, testCase.targetModel, modelRequest.Model)
				assert.Equal(t, testCase.targetModel, extractModelNameFromGeminiPath(c.Request.URL.Path))
				assert.Equal(t, testCase.wantRawQuery, c.Request.URL.RawQuery)
				assert.Equal(t, c.Request.URL.RequestURI(), c.Request.RequestURI)
				assert.True(t, keyed)
			} else {
				assert.Equal(t, tokenMappingClientModel, modelRequest.Model)
				assert.Equal(t, originalRequestURI, c.Request.RequestURI)
				assert.False(t, keyed)
			}
		})
	}
}

func TestApplyTokenModelMappingRewritesRealtimeQuery(t *testing.T) {
	c := newTokenModelMappingContext(http.MethodPost, "/v1/realtime?model=claude-opus-4-8&x=1", "", "",
		map[string]string{tokenMappingClientModel: tokenMappingLogical})
	modelRequest := &ModelRequest{Model: tokenMappingClientModel}
	require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

	assert.Equal(t, tokenMappingLogical, modelRequest.Model)
	assert.Equal(t, tokenMappingLogical, c.Request.URL.Query().Get("model"))
	assert.Equal(t, "1", c.Request.URL.Query().Get("x"))
	assert.Equal(t, c.Request.URL.RequestURI(), c.Request.RequestURI)
	assert.Equal(t, tokenMappingClientModel, common.GetContextKeyString(c, constant.ContextKeyTokenModelMappingClientModel))

	t.Run("without model query", func(t *testing.T) {
		c := newTokenModelMappingContext(http.MethodPost, "/v1/realtime?x=1", "", "",
			map[string]string{tokenMappingClientModel: tokenMappingLogical})
		modelRequest := &ModelRequest{Model: tokenMappingClientModel}
		require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

		assert.Equal(t, tokenMappingClientModel, modelRequest.Model)
		assert.Equal(t, "x=1", c.Request.URL.RawQuery)
		_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
		assert.False(t, keyed)
	})
}

func TestApplyTokenModelMappingRewritesURLEncodedForm(t *testing.T) {
	c := newTokenModelMappingContext(http.MethodPost, "/v1/chat/completions", "model=claude-opus-4-8&prompt=hi",
		gin.MIMEPOSTForm, map[string]string{tokenMappingClientModel: tokenMappingLogical})
	modelRequest := &ModelRequest{Model: tokenMappingClientModel}
	require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

	assert.Equal(t, tokenMappingLogical, modelRequest.Model)
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	rewritten, err := storage.Bytes()
	require.NoError(t, err)
	values, err := url.ParseQuery(string(rewritten))
	require.NoError(t, err)
	assert.Equal(t, tokenMappingLogical, values.Get("model"))
	assert.Equal(t, "hi", values.Get("prompt"))
	assert.Equal(t, int64(len(rewritten)), c.Request.ContentLength)

	t.Run("without model field", func(t *testing.T) {
		c := newTokenModelMappingContext(http.MethodPost, "/v1/chat/completions", "prompt=hi",
			gin.MIMEPOSTForm, map[string]string{tokenMappingClientModel: tokenMappingLogical})
		modelRequest := &ModelRequest{Model: tokenMappingClientModel}
		require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

		assert.Equal(t, tokenMappingClientModel, modelRequest.Model)
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		untouched, err := storage.Bytes()
		require.NoError(t, err)
		assert.Equal(t, []byte("prompt=hi"), untouched)
		_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
		assert.False(t, keyed)
	})
}

func TestApplyTokenModelMappingNoopWithoutMappingOrOnMiss(t *testing.T) {
	body := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`

	tests := []struct {
		name    string
		mapping map[string]string
		model   string
	}{
		{name: "no mapping configured", model: tokenMappingClientModel},
		{name: "mapping miss", mapping: map[string]string{"other-model": tokenMappingLogical}, model: tokenMappingClientModel},
		{name: "self mapping", mapping: map[string]string{tokenMappingClientModel: tokenMappingClientModel}, model: tokenMappingClientModel},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c := newTokenModelMappingContext(http.MethodPost, "/v1/chat/completions", body, "application/json", testCase.mapping)
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			before, err := storage.Bytes()
			require.NoError(t, err)
			contentLength := c.Request.ContentLength

			modelRequest := &ModelRequest{Model: testCase.model}
			require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

			assert.Equal(t, testCase.model, modelRequest.Model)
			after, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			unchanged, err := after.Bytes()
			require.NoError(t, err)
			assert.Equal(t, before, unchanged)
			// The original storage stays open: nothing was replaced.
			_, err = storage.Bytes()
			assert.NoError(t, err)
			assert.Equal(t, contentLength, c.Request.ContentLength)
			_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
			assert.False(t, keyed)
		})
	}
}

func TestApplyTokenModelMappingFailSafeSources(t *testing.T) {
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	require.NoError(t, writer.WriteField("model", tokenMappingClientModel))
	require.NoError(t, writer.WriteField("prompt", "hi"))
	require.NoError(t, writer.Close())

	tests := []struct {
		name        string
		target      string
		body        string
		contentType string
		prepare     func(*gin.Context)
	}{
		{name: "multipart form", target: "/v1/audio/transcriptions", body: multipartBody.String(), contentType: writer.FormDataContentType()},
		{name: "json without model", target: "/v1/chat/completions", body: `{"messages":[]}`, contentType: "application/json"},
		{name: "json model is not a string", target: "/v1/chat/completions", body: `{"model":123}`, contentType: "application/json"},
		{
			name: "task plugin claimed model", target: "/v1/chat/completions", body: `{"model":"claude-opus-4-8"}`,
			contentType: "application/json",
			prepare:     func(c *gin.Context) { c.Set("resolved_task_model", tokenMappingClientModel) },
		},
		{name: "midjourney action", target: "/mj/submit/imagine", body: `{"model":"claude-opus-4-8","prompt":"cat"}`, contentType: "application/json"},
		{name: "embeddings path without body model", target: "/v1/engines/claude-opus-4-8/embeddings", body: "", contentType: "application/json"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c := newTokenModelMappingContext(http.MethodPost, testCase.target, testCase.body, testCase.contentType,
				map[string]string{tokenMappingClientModel: tokenMappingLogical})
			if testCase.prepare != nil {
				testCase.prepare(c)
			}
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			before, err := storage.Bytes()
			require.NoError(t, err)
			contentLength := c.Request.ContentLength
			path := c.Request.URL.Path

			modelRequest := &ModelRequest{Model: tokenMappingClientModel}
			require.NoError(t, ApplyTokenModelMapping(c, modelRequest))

			assert.Equal(t, tokenMappingClientModel, modelRequest.Model)
			after, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			unchanged, err := after.Bytes()
			require.NoError(t, err)
			assert.Equal(t, before, unchanged)
			assert.Equal(t, contentLength, c.Request.ContentLength)
			assert.Equal(t, path, c.Request.URL.Path)
			_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
			assert.False(t, keyed)
		})
	}
}

func TestDistributeAppliesTokenModelMappingBeforeLimitsAndAutoGroups(t *testing.T) {
	require.NoError(t, appI18n.Init())
	db := setupTokenMappingChannelSelectDB(t)
	createTokenMappingChannel(t, db, 4101, "group-a", tokenMappingLogical, "")
	createTokenMappingChannel(t, db, 4102, "group-b", tokenMappingLogical, "")
	model.InitChannelCache()

	terminalReached := false
	router := newTokenMappingDistributeRouter(tokenMappingDistributeSetup{
		mapping:     map[string]string{tokenMappingClientModel: tokenMappingLogical},
		modelLimits: true,
		limitModels: map[string]bool{tokenMappingLogical: true},
	}, func(c *gin.Context) {
		terminalReached = true
		assert.Equal(t, tokenMappingLogical, c.GetString("original_model"))
		assert.Equal(t, 4101, c.GetInt("channel_id"))
		assert.Equal(t, "group-a", common.GetContextKeyString(c, constant.ContextKeyAutoGroup))
		var decoded struct{ Model string }
		require.NoError(t, common.UnmarshalBodyReusable(c, &decoded))
		assert.Equal(t, tokenMappingLogical, decoded.Model)
		assert.Equal(t, tokenMappingClientModel, common.GetContextKeyString(c, constant.ContextKeyTokenModelMappingClientModel))
		c.Status(http.StatusNoContent)
	})

	recorder := performTokenMappingRequest(t, router, `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`)

	assert.True(t, terminalReached)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestDistributeTokenModelMappingRejectsWithoutMapping(t *testing.T) {
	require.NoError(t, appI18n.Init())
	db := setupTokenMappingChannelSelectDB(t)
	createTokenMappingChannel(t, db, 4201, "group-a", tokenMappingLogical, "")
	createTokenMappingChannel(t, db, 4202, "group-b", tokenMappingLogical, "")
	model.InitChannelCache()

	const body = `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`

	t.Run("token model limits reject the client model", func(t *testing.T) {
		router := newTokenMappingDistributeRouter(tokenMappingDistributeSetup{
			modelLimits: true,
			limitModels: map[string]bool{tokenMappingLogical: true},
		}, func(c *gin.Context) {
			t.Error("the request must stop before the relay handler")
			c.Status(http.StatusNoContent)
		})

		recorder := performTokenMappingRequest(t, router, body)

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Contains(t, recorder.Body.String(),
			appI18n.Translate(appI18n.LangEn, appI18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": tokenMappingClientModel}))
	})

	t.Run("channel abilities never match the client model", func(t *testing.T) {
		router := newTokenMappingDistributeRouter(tokenMappingDistributeSetup{}, func(c *gin.Context) {
			t.Error("the request must stop before the relay handler")
			c.Status(http.StatusNoContent)
		})

		recorder := performTokenMappingRequest(t, router, body)

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.Contains(t, recorder.Body.String(),
			appI18n.Translate(appI18n.LangEn, appI18n.MsgDistributorNoAvailableChannel, map[string]any{"Group": "auto", "Model": tokenMappingClientModel}))
	})
}

func TestDistributeTokenModelMappingCycleReturns400(t *testing.T) {
	require.NoError(t, appI18n.Init())
	router := newTokenMappingDistributeRouter(tokenMappingDistributeSetup{
		mapping: map[string]string{"a": "b", "b": "a"},
	}, func(c *gin.Context) {
		t.Error("the request must stop before the relay handler")
		c.Status(http.StatusNoContent)
	})

	recorder := performTokenMappingRequest(t, router, `{"model":"a","messages":[{"role":"user","content":"hi"}]}`)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(),
		appI18n.Translate(appI18n.LangEn, appI18n.MsgDistributorTokenModelMappingCycle, map[string]any{"Model": "a"}))
	assert.Contains(t, recorder.Body.String(), `"code":"invalid_request"`)
}

func TestDistributeSkipsTokenModelMappingWithoutChannelSelection(t *testing.T) {
	require.NoError(t, appI18n.Init())

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "video remix", method: http.MethodPost, target: "/v1/videos/video-1/remix", body: `{"model":"claude-opus-4-8"}`},
		{name: "video fetch", method: http.MethodGet, target: "/v1/videos/video-1", body: ""},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			terminalReached := false
			router := gin.New()
			handler := func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyTokenModelMapping, map[string]string{tokenMappingClientModel: tokenMappingLogical})
				c.Set("id", 1)
			}
			router.Handle(testCase.method, testCase.target, RequestId(), handler, Distribute(), func(c *gin.Context) {
				terminalReached = true
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				raw, err := storage.Bytes()
				require.NoError(t, err)
				assert.Equal(t, []byte(testCase.body), raw)
				assert.Empty(t, c.GetString("original_model"))
				_, keyed := c.Get(string(constant.ContextKeyTokenModelMappingClientModel))
				assert.False(t, keyed)
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(testCase.method, testCase.target, strings.NewReader(testCase.body))
			if testCase.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			assert.True(t, terminalReached)
			assert.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestDistributeTokenMappingFeedsChannelModelMapping(t *testing.T) {
	require.NoError(t, appI18n.Init())
	db := setupTokenMappingChannelSelectDB(t)
	createTokenMappingChannel(t, db, 4301, "group-a", tokenMappingLogical, `{"dsv4f":"deepseek-v4.1-flash"}`)
	model.InitChannelCache()

	router := newTokenMappingDistributeRouter(tokenMappingDistributeSetup{
		mapping:     map[string]string{tokenMappingClientModel: tokenMappingLogical},
		modelLimits: true,
		limitModels: map[string]bool{tokenMappingLogical: true},
	}, func(c *gin.Context) {
		request, err := helper.GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		info := relaycommon.GenRelayInfoOpenAI(c, request)
		assert.Equal(t, tokenMappingLogical, info.OriginModelName)
		info.InitChannelMeta(c)
		require.NoError(t, helper.ModelMappedHelper(c, info, request))
		assert.Equal(t, "deepseek-v4.1-flash", info.UpstreamModelName)
		assert.Equal(t, "deepseek-v4.1-flash", request.Model)
		assert.True(t, info.IsModelMapped)
		c.Status(http.StatusNoContent)
	})

	recorder := performTokenMappingRequest(t, router, `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

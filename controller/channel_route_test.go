package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelRouteTestDB(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel-route.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}))
}

// callChannelRouteHandler 用 root 身份调用渠道 handler；idParam 非空时填充路由参数 :id。
func callChannelRouteHandler(t *testing.T, method string, target string, body any, idParam string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, recorder := newAuthenticatedContext(t, method, target, body, 1)
	ctx.Set("role", common.RoleRootUser)
	if idParam != "" {
		ctx.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	handler(ctx)
	return recorder
}

func TestUpdateChannelIgnoresClientRouteKey(t *testing.T) {
	setupChannelRouteTestDB(t)

	channel := &model.Channel{Name: "route-key-update", Type: 1, Key: "sk-update", Models: "gpt-4o", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, channel.Insert())
	origin, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.True(t, model.IsValidChannelRouteKey(origin.GetRouteKey()))

	recorder := callChannelRouteHandler(t, http.MethodPut, "/api/channel/", map[string]any{
		"id":                  channel.Id,
		"name":                "route-key-update-renamed",
		"type":                1,
		"models":              "gpt-4o",
		"group":               "default",
		"route_key":           "ch_clientSupplied01",
		"base_url":            "",
		"openai_organization": "",
	}, "", UpdateChannel)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "route-key-update-renamed", stored.Name)
	assert.Equal(t, origin.GetRouteKey(), stored.GetRouteKey())
}

func TestAddChannelAssignsServerRouteKeysInBatchMode(t *testing.T) {
	setupChannelRouteTestDB(t)

	recorder := callChannelRouteHandler(t, http.MethodPost, "/api/channel/", map[string]any{
		"mode": "batch",
		"channel": map[string]any{
			"name":      "route-key-batch",
			"type":      1,
			"key":       "sk-batch-one\nsk-batch-two",
			"models":    "gpt-4o",
			"group":     "default",
			"route_key": "ch_clientSupplied01",
		},
	}, "", AddChannel)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())

	var stored []model.Channel
	require.NoError(t, model.DB.Order("id ASC").Find(&stored).Error)
	require.Len(t, stored, 2)

	assert.NotEqual(t, "ch_clientSupplied01", stored[0].GetRouteKey())
	for _, channel := range stored {
		assert.Truef(t, model.IsValidChannelRouteKey(channel.GetRouteKey()),
			"channel %d has invalid route key %q", channel.Id, channel.GetRouteKey())
	}
	assert.NotEqual(t, stored[0].GetRouteKey(), stored[1].GetRouteKey())
}

func TestCopyChannelGeneratesNewRouteKey(t *testing.T) {
	setupChannelRouteTestDB(t)

	origin := &model.Channel{Name: "route-key-origin", Type: 1, Key: "sk-origin", Models: "gpt-4o", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, origin.Insert())
	loaded, err := model.GetChannelById(origin.Id, true)
	require.NoError(t, err)

	recorder := callChannelRouteHandler(t, http.MethodPost, "/api/channel/copy/"+strconv.Itoa(origin.Id), nil, strconv.Itoa(origin.Id), CopyChannel)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Id int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())

	copied, err := model.GetChannelById(response.Data.Id, true)
	require.NoError(t, err)
	assert.True(t, model.IsValidChannelRouteKey(copied.GetRouteKey()))
	assert.NotEqual(t, loaded.GetRouteKey(), copied.GetRouteKey())
}

func TestGetChannelRouteOptionsReturnsReadOnlyProjection(t *testing.T) {
	setupChannelRouteTestDB(t)

	first := &model.Channel{Name: "route-options-first", Type: 3, Key: "sk-first", Models: "gpt-4o", Group: "default", Status: common.ChannelStatusManuallyDisabled}
	require.NoError(t, first.Insert())
	tag := "route-options"
	second := &model.Channel{Name: "route-options-second", Type: 1, Key: "sk-second", Models: "gpt-4o", Group: "default", Status: common.ChannelStatusEnabled, Tag: &tag}
	require.NoError(t, second.Insert())

	recorder := callChannelRouteHandler(t, http.MethodGet, "/api/channel/route_options", nil, "", GetChannelRouteOptions)
	var response struct {
		Success bool `json:"success"`
		Data    []struct {
			Id       int     `json:"id"`
			RouteKey *string `json:"route_key"`
			Name     string  `json:"name"`
			Type     int     `json:"type"`
			Status   int     `json:"status"`
			Tag      *string `json:"tag"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	require.Len(t, response.Data, 2)

	assert.Equal(t, first.Id, response.Data[0].Id)
	assert.Equal(t, second.Id, response.Data[1].Id)
	assert.Equal(t, "route-options-first", response.Data[0].Name)
	assert.Equal(t, 3, response.Data[0].Type)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, response.Data[0].Status)
	assert.Nil(t, response.Data[0].Tag)
	assert.Equal(t, &tag, response.Data[1].Tag)
	for _, option := range response.Data {
		require.NotNil(t, option.RouteKey)
		assert.True(t, model.IsValidChannelRouteKey(*option.RouteKey))
	}

	body := recorder.Body.String()
	assert.NotContains(t, body, "sk-first")
	assert.NotContains(t, body, "sk-second")
	assert.NotContains(t, body, `"key"`)
	assert.NotContains(t, body, `"models"`)
	assert.NotContains(t, body, `"settings"`)
}

package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	tokenMappingClientModel  = "claude-opus-4-8"
	tokenMappingLogicalModel = "dsv4f"
)

// The mock upstream routes by the base-url path prefix of each channel, so a
// recorded call identifies which channel served it without reading the database.
const (
	tokenMappingFengwindPath  = "/fengwind"
	tokenMappingAgentPath     = "/agent"
	tokenMappingQQPath        = "/qq"
	tokenMappingMiscPath      = "/misc"
	tokenMappingFengwindModel = "deepseek-v4.1-flash"
	tokenMappingAgentModel    = "deepseek-v4-flash"
	tokenMappingQQModel       = "DeepSeek-V4-Flash-0731-think"
)

type tokenMappingUpstreamCall struct {
	Path  string
	Model string
	Body  []byte
}

type tokenMappingUpstream struct {
	url string
	mu  sync.Mutex
	// calls holds the requests received since the last takeCalls.
	calls      []tokenMappingUpstreamCall
	failPrefix string
}

func newTokenMappingUpstream(t *testing.T) *tokenMappingUpstream {
	t.Helper()
	upstream := &tokenMappingUpstream{}
	server := httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func (u *tokenMappingUpstream) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusInternalServerError)
		return
	}
	model := gjson.GetBytes(body, "model").String()
	u.mu.Lock()
	u.calls = append(u.calls, tokenMappingUpstreamCall{Path: r.URL.Path, Model: model, Body: body})
	fail := u.failPrefix != "" && strings.HasPrefix(r.URL.Path, u.failPrefix)
	u.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"mock upstream failure","type":"server_error"}}`)
		return
	}
	response, err := common.Marshal(map[string]any{
		"id":     "chatcmpl-1",
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": "ok"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
	})
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(response)
}

// failPath makes every request below prefix answer with 500 until it is cleared.
func (u *tokenMappingUpstream) failPath(prefix string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.failPrefix = prefix
}

// takeCalls returns and clears the requests received since the previous call.
func (u *tokenMappingUpstream) takeCalls() []tokenMappingUpstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	calls := u.calls
	u.calls = nil
	return calls
}

type tokenModelMappingChannels struct {
	fengwind *model.Channel
	agent    *model.Channel
	qq       *model.Channel
	misc     *model.Channel
}

type tokenModelMappingFixture struct {
	upstream *tokenMappingUpstream
	engine   *gin.Engine
	user     *model.User
	// ccDsv4f maps the Claude client model onto the dsv4f logical model, which
	// three channel-level redirects then translate into provider model names.
	ccDsv4f  *model.Token
	ccF51    *model.Token
	ccOpus5  *model.Token
	plain    *model.Token
	ccWrong  *model.Token
	channels tokenModelMappingChannels
}

// newTokenModelMappingFixture starts the real relay path (TokenAuth, Distribute,
// Relay) on top of the shared responses-WebSocket database fixture.
func newTokenModelMappingFixture(t *testing.T) *tokenModelMappingFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	require.NoError(t, i18n.Init())

	user, _ := setupResponsesWSRequestTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.UserSubscription{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	require.NoError(t, model.DB.Model(user).Update("quota", 1_000_000).Error)

	previousBatch := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	previousRetryTimes := common.RetryTimes
	previousCountToken := constant.CountToken
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	previousCompletionRatios := ratio_setting.CompletionRatio2JSONString()
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousMaxAutoGroups := setting.GetMaxTokenAutoGroups()
	globalSettings := model_setting.GetGlobalSettings()
	previousGlobalSettings := *globalSettings
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatch
		common.LogConsumeEnabled = previousLogConsume
		common.RetryTimes = previousRetryTimes
		constant.CountToken = previousCountToken
		*globalSettings = previousGlobalSettings
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletionRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(strconv.Itoa(previousMaxAutoGroups)))
	})

	// Explicit ratios keep ModelPriceHelper and ListModels deterministic without
	// enabling self-use mode; model_setting is pinned because other tests in the
	// package can leave the global relay switches in a non-default state.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"dsv4f":1,"f5.1":1,"opus5":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"dsv4f":1,"f5.1":1,"opus5":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":1,"rt-dsv4f-fengwind":1,"rt-dsv4f-agent":1,"rt-dsv4f-qq":1,"rt-misc":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"default":"Default","auto":"Auto","rt-dsv4f-fengwind":"F","rt-dsv4f-agent":"A","rt-dsv4f-qq":"Q","rt-misc":"M"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("5"))
	common.BatchUpdateEnabled, common.LogConsumeEnabled = false, true
	common.RetryTimes, constant.CountToken = 1, false
	globalSettings.PassThroughRequestEnabled = false
	globalSettings.ChatCompletionsToResponsesPolicy.Enabled = false

	fixture := &tokenModelMappingFixture{user: user, upstream: newTokenMappingUpstream(t)}
	fixture.channels = tokenModelMappingChannels{
		fengwind: fixture.insertChannel(t, "FENGWIND-DS", "rt-dsv4f-fengwind", tokenMappingLogicalModel, tokenMappingFengwindPath,
			map[string]string{tokenMappingLogicalModel: tokenMappingFengwindModel}),
		agent: fixture.insertChannel(t, "AR-DS-A1", "rt-dsv4f-agent", tokenMappingLogicalModel, tokenMappingAgentPath,
			map[string]string{tokenMappingLogicalModel: tokenMappingAgentModel}),
		qq: fixture.insertChannel(t, "QQ-DS", "rt-dsv4f-qq", tokenMappingLogicalModel, tokenMappingQQPath,
			map[string]string{tokenMappingLogicalModel: tokenMappingQQModel}),
		misc: fixture.insertChannel(t, "MISC", "rt-misc", "f5.1,opus5", tokenMappingMiscPath, nil),
	}
	fixture.ccDsv4f = fixture.insertToken(t, "cc-dsv4f", "ccdsv4fmapped", "auto",
		map[string]string{tokenMappingClientModel: tokenMappingLogicalModel}, true, tokenMappingLogicalModel)
	fixture.plain = fixture.insertToken(t, "plain", "plainunmapped", "auto", nil, false, "")
	fixture.ccF51 = fixture.insertToken(t, "cc-f51", "ccf51mapped", "rt-misc",
		map[string]string{tokenMappingClientModel: "f5.1"}, false, "")
	fixture.ccOpus5 = fixture.insertToken(t, "cc-opus5", "ccopus5mapped", "rt-misc",
		map[string]string{tokenMappingClientModel: "opus5"}, false, "")
	fixture.ccWrong = fixture.insertToken(t, "cc-wronglimit", "ccwronglimit", "auto",
		map[string]string{tokenMappingClientModel: tokenMappingLogicalModel}, true, "opus5")

	fixture.engine = gin.New()
	fixture.engine.POST("/v1/messages", middleware.RequestId(), middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) {
		Relay(c, types.RelayFormatClaude)
	})
	fixture.engine.GET("/v1/models", middleware.RequestId(), middleware.TokenAuth(), func(c *gin.Context) {
		ListModels(c, constant.ChannelTypeOpenAI)
	})
	return fixture
}

func (f *tokenModelMappingFixture) insertChannel(t *testing.T, name, group, models, path string, mapping map[string]string) *model.Channel {
	t.Helper()
	baseURL := f.upstream.url + path
	channel := &model.Channel{
		Name: name, Type: constant.ChannelTypeOpenAI, Key: "sk-mock",
		Status: common.ChannelStatusEnabled, Group: group, Models: models, BaseURL: &baseURL,
	}
	if len(mapping) > 0 {
		raw, err := common.Marshal(mapping)
		require.NoError(t, err)
		channel.ModelMapping = common.GetPointer(string(raw))
	}
	require.NoError(t, channel.Insert())
	return channel
}

func (f *tokenModelMappingFixture) insertToken(t *testing.T, name, key, group string, mapping map[string]string, limitsEnabled bool, limits string) *model.Token {
	t.Helper()
	token := &model.Token{
		UserId: f.user.Id, Name: name, Key: key, Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, Group: group, CrossGroupRetry: true,
		ModelLimitsEnabled: limitsEnabled, ModelLimits: limits,
	}
	require.NoError(t, token.SetModelMapping(mapping))
	if group == "auto" {
		require.NoError(t, token.SetAutoGroups([]string{"rt-dsv4f-fengwind", "rt-dsv4f-agent", "rt-dsv4f-qq"}))
	}
	require.NoError(t, token.Insert())
	return token
}

func (f *tokenModelMappingFixture) postMessages(t *testing.T, token *model.Token, modelName string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`, modelName)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", "sk-"+token.Key)
	request.Header.Set("anthropic-version", "2023-06-01")
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, request)
	return recorder
}

func (f *tokenModelMappingFixture) getModels(t *testing.T, token *model.Token) []string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer sk-"+token.Key)
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, request)
	payload := decodeListModelsPayload(t, recorder)
	ids := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		ids = append(ids, item.Id)
	}
	return ids
}

// disableChannel takes a channel out of routing, both as a channel state and in
// the ability rows the distributor actually selects from.
func (f *tokenModelMappingFixture) disableChannel(t *testing.T, channel *model.Channel) {
	t.Helper()
	require.NoError(t, model.DB.Model(channel).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, model.UpdateAbilityStatus(channel.Id, false))
}

func (f *tokenModelMappingFixture) enablePassThroughBody(t *testing.T, channel *model.Channel) {
	t.Helper()
	channel.SetSetting(dto.ChannelSettings{PassThroughBodyEnabled: true})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
		Update("setting", *channel.Setting).Error)
}

func lastConsumeLog(t *testing.T, token *model.Token) model.Log {
	t.Helper()
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", token.Id, model.LogTypeConsume).
		Order("id desc").First(&log).Error)
	return log
}

func logOther(t *testing.T, log model.Log) map[string]any {
	t.Helper()
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	return other
}

func logAdminInfo(t *testing.T, other map[string]any) map[string]any {
	t.Helper()
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok, "consume log must carry admin_info: %v", other)
	return adminInfo
}

// TestTokenModelMappingEndToEnd covers the full client-model -> logical model ->
// provider-model chain of the token-level model redirect through the real HTTP
// relay: routing by the logical model, channel-level redirect, consume-log
// fields, token model limits, the model catalog, and cross-group fallback.
func TestTokenModelMappingEndToEnd(t *testing.T) {
	t.Run("S1_claude_code_mapped_to_logical_model", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)

		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var claudeResponse dto.ClaudeResponse
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &claudeResponse))
		assert.Equal(t, "message", claudeResponse.Type)

		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingFengwindPath+"/"), calls[0].Path)
		assert.Equal(t, tokenMappingFengwindModel, calls[0].Model)

		log := lastConsumeLog(t, fixture.ccDsv4f)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		other := logOther(t, log)
		assert.Equal(t, true, other["is_model_mapped"])
		assert.Equal(t, tokenMappingFengwindModel, other["upstream_model_name"])
		assert.Equal(t, tokenMappingClientModel, other["client_model"])
		assert.NotContains(t, logAdminInfo(t, other), "client_model")
	})

	t.Run("S2_models_catalog_lists_logical_models_only", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)

		unmapped := fixture.getModels(t, fixture.plain)
		require.Contains(t, unmapped, tokenMappingLogicalModel)
		assert.NotContains(t, unmapped, tokenMappingClientModel)

		limited := fixture.getModels(t, fixture.ccDsv4f)
		assert.Equal(t, []string{tokenMappingLogicalModel}, limited)
	})

	t.Run("S3_cross_group_retry_follows_auto_group_order", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)

		fixture.disableChannel(t, fixture.channels.fengwind)
		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingAgentPath+"/"), calls[0].Path)
		assert.Equal(t, tokenMappingAgentModel, calls[0].Model)
		log := lastConsumeLog(t, fixture.ccDsv4f)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		assert.Contains(t, logAdminInfo(t, logOther(t, log))["use_channel"], strconv.Itoa(fixture.channels.agent.Id))

		fixture.disableChannel(t, fixture.channels.agent)
		response = fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls = fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingQQPath+"/"), calls[0].Path)
		assert.Equal(t, tokenMappingQQModel, calls[0].Model)
		log = lastConsumeLog(t, fixture.ccDsv4f)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		assert.Contains(t, logAdminInfo(t, logOther(t, log))["use_channel"], strconv.Itoa(fixture.channels.qq.Id))

		fixture.disableChannel(t, fixture.channels.qq)
		response = fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), tokenMappingLogicalModel)
		assert.NotContains(t, response.Body.String(), tokenMappingClientModel)
		assert.Empty(t, fixture.upstream.takeCalls())
	})

	t.Run("S3b_upstream_500_retries_into_next_group", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		fixture.upstream.failPath(tokenMappingFengwindPath)

		// A failing upstream retries inside its group first (the group has a
		// single priority, so the retry index selects it again) and only then
		// continues with the next Auto group; the token redirect stays in effect
		// for the channel that finally answers.
		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 3)
		for index, want := range []struct{ path, model string }{
			{path: tokenMappingFengwindPath, model: tokenMappingFengwindModel},
			{path: tokenMappingFengwindPath, model: tokenMappingFengwindModel},
			{path: tokenMappingAgentPath, model: tokenMappingAgentModel},
		} {
			assert.True(t, strings.HasPrefix(calls[index].Path, want.path+"/"), "call %d: %s", index, calls[index].Path)
			assert.Equal(t, want.model, calls[index].Model, "call %d", index)
		}
		log := lastConsumeLog(t, fixture.ccDsv4f)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		other := logOther(t, log)
		assert.Equal(t, tokenMappingClientModel, other["client_model"])
		assert.Equal(t, []any{
			strconv.Itoa(fixture.channels.fengwind.Id),
			strconv.Itoa(fixture.channels.fengwind.Id),
			strconv.Itoa(fixture.channels.agent.Id),
		}, logAdminInfo(t, other)["use_channel"])
	})

	t.Run("S4_same_client_model_different_keys", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		for _, tc := range []struct {
			name  string
			token *model.Token
			model string
		}{
			{name: "f5.1", token: fixture.ccF51, model: "f5.1"},
			{name: "opus5", token: fixture.ccOpus5, model: "opus5"},
		} {
			response := fixture.postMessages(t, tc.token, tokenMappingClientModel)
			require.Equal(t, http.StatusOK, response.Code, "%s: %s", tc.name, response.Body.String())
			calls := fixture.upstream.takeCalls()
			require.Len(t, calls, 1)
			assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingMiscPath+"/"), calls[0].Path)
			assert.Equal(t, tc.model, calls[0].Model, "the misc channel has no channel-level redirect")
			log := lastConsumeLog(t, tc.token)
			assert.Equal(t, tc.model, log.ModelName)
			assert.Equal(t, tokenMappingClientModel, logOther(t, log)["client_model"])
		}
	})

	t.Run("S5_model_limits_apply_to_logical_model", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)

		response := fixture.postMessages(t, fixture.ccWrong, tokenMappingClientModel)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), tokenMappingLogicalModel)
		assert.NotContains(t, response.Body.String(), tokenMappingClientModel)
		assert.Empty(t, fixture.upstream.takeCalls())
	})

	t.Run("S6_unmapped_token_behaviour_unchanged", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)

		response := fixture.postMessages(t, fixture.plain, tokenMappingLogicalModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, tokenMappingFengwindModel, calls[0].Model, "channel-level redirect still applies")
		log := lastConsumeLog(t, fixture.plain)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		assert.NotContains(t, logOther(t, log), "client_model")

		response = fixture.postMessages(t, fixture.plain, tokenMappingClientModel)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), tokenMappingClientModel)
		assert.Empty(t, fixture.upstream.takeCalls())
	})

	t.Run("S9_passthrough_body_carries_logical_model", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		fixture.enablePassThroughBody(t, fixture.channels.fengwind)

		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingFengwindPath+"/"), calls[0].Path)
		// The passthrough body is the request storage already rewritten by the
		// token mapping; the channel-level redirect only changes RelayInfo, which
		// is the existing behaviour of pass-through channels.
		assert.Equal(t, tokenMappingLogicalModel, calls[0].Model)
		assert.NotContains(t, string(calls[0].Body), tokenMappingClientModel)
		assert.Equal(t, tokenMappingLogicalModel, lastConsumeLog(t, fixture.ccDsv4f).ModelName)
	})

	t.Run("Redis_enabled_variant", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		redisServer := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		common.RedisEnabled, common.RDB = true, client

		// Warm the token hash with the original redirect before rotating it.
		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, tokenMappingFengwindModel, calls[0].Model)

		require.NoError(t, fixture.ccDsv4f.SetModelMapping(map[string]string{tokenMappingClientModel: "f5.1"}))
		fixture.ccDsv4f.Group = "rt-misc"
		fixture.ccDsv4f.ModelLimitsEnabled = false
		fixture.ccDsv4f.ModelLimits = ""
		require.NoError(t, fixture.ccDsv4f.Update())

		response = fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls = fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingMiscPath+"/"), calls[0].Path)
		assert.Equal(t, "f5.1", calls[0].Model, "the rotated redirect must apply immediately")

		response = fixture.postMessages(t, fixture.plain, tokenMappingLogicalModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, logOther(t, lastConsumeLog(t, fixture.plain)), "client_model")
		response = fixture.postMessages(t, fixture.plain, tokenMappingClientModel)
		assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	})
}

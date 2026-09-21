package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRetryRelayErrorHonorsChannelPinOnChannelError(t *testing.T) {
	err := types.NewError(errors.New("channel failed"), types.ErrorCodeChannelNoAvailableKey)
	for _, test := range []struct {
		name      string
		pin       *dto.ChannelPin
		wantRetry bool
	}{
		{name: "unrestricted channel error", wantRetry: true},
		{
			name: "single attempt pin suppresses channel error retry",
			pin: &dto.ChannelPin{
				ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt,
			},
			wantRetry: false,
		},
		{
			name: "origin task pin permits retry on the same channel",
			pin: &dto.ChannelPin{
				ChannelId: 1, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel,
			},
			wantRetry: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if test.pin != nil {
				GetChannelConstraints(c).AddPin(*test.pin)
			}
			assert.Equal(t, test.wantRetry, ShouldRetryRelayError(c, err, 1))
		})
	}
}

func TestProcessChannelErrorMasksDisableReasonAndNotification(t *testing.T) {
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousAutoDisable, previousErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	previousNotifyLimit := constant.NotifyLimitCount
	previousClient, previousWorker := httpClient, system_setting.WorkerUrl
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = previousAutoDisable, previousErrorLog
		constant.NotifyLimitCount = previousNotifyLimit
		httpClient, system_setting.WorkerUrl = previousClient, previousWorker
		*fetch = previousFetch
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, false
	constant.NotifyLimitCount = 10
	channel := &model.Channel{Name: "relay-review", Key: "fixture-key", Type: 1, Status: common.ChannelStatusEnabled, Group: "default", Models: "test-model"}
	require.NoError(t, channel.Insert())
	notifications := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		notifications <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	httpClient, system_setting.WorkerUrl = server.Client(), ""
	fetch.EnableSSRFProtection = false
	settings, err := common.Marshal(kitdto.UserSetting{NotifyType: kitdto.NotifyTypeWebhook, WebhookUrl: server.URL})
	require.NoError(t, err)
	root := &model.User{Username: "notification-test-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Setting: string(settings)}
	require.NoError(t, database.Create(root).Error)
	notifyKey := fmt.Sprintf("%d:%s:%s", root.Id, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), time.Now().Format("2006010215"))
	notifyLimitStore.Delete(notifyKey)
	t.Cleanup(func() { notifyLimitStore.Delete(notifyKey) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream https://private.example.com/path?token=review-token api_key:review-secret"), types.ErrorCodeChannelNoAvailableKey, http.StatusUnauthorized)
	ProcessChannelError(c, types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, AutoBan: true}, apiErr, nil)
	var notification WebhookPayload
	select {
	case payload := <-notifications:
		require.NoError(t, common.Unmarshal(payload, &notification))
	case <-time.After(5 * time.Second):
		t.Fatal("automatic channel-disable notification was not delivered")
	}
	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, loaded.Status)
	wantReason := "status_code=401, upstream https://***.com/***?token=*** api_key:***"
	assert.Equal(t, wantReason, loaded.GetOtherInfo()["status_reason"])
	assert.Contains(t, notification.Content, wantReason)
	assert.NotContains(t, notification.Content, "review-token")
	assert.NotContains(t, notification.Content, "review-secret")
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestDecideRelayRetryReasons(t *testing.T) {
	previousMaxTotalAttempts := common.MaxTotalAttempts
	t.Cleanup(func() { common.MaxTotalAttempts = previousMaxTotalAttempts })
	upstream := func(status int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, status)
	}
	for _, tc := range []struct {
		name    string
		err     *types.NewAPIError
		retries int
		setup   func(*gin.Context)
		want    PolicyDecision
	}{
		{name: "retry status matched", err: upstream(http.StatusTooManyRequests), retries: 1, want: PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}},
		{name: "status outside retry rules", err: upstream(http.StatusBadRequest), retries: 1, want: PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}},
		{name: "attempt budget exhausted", err: upstream(http.StatusTooManyRequests), retries: 0, want: PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}},
		{name: "always skipped status", err: upstream(http.StatusGatewayTimeout), retries: 1, want: PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}},
		{name: "success status never retries", err: upstream(http.StatusOK), retries: 1, want: PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}},
		{name: "skip retry error", err: types.NewErrorWithStatusCode(errors.New("local"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry()), retries: 1, want: PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}},
		{name: "channel error retries without budget", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), retries: 0, want: PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}},
		{name: "single attempt pin", err: upstream(http.StatusTooManyRequests), retries: 1, setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
		}, want: PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}},
		{name: "strict session", err: upstream(http.StatusTooManyRequests), retries: 1, setup: func(c *gin.Context) {
			c.Set(ginKeyChannelAffinitySkipRetry, true)
			RequestPolicy(c).SessionModeSource = "global"
		}, want: PolicyDecision{Action: "stop", Reason: "strict_session", Source: "global"}},
		{name: "nil error", retries: 1, want: PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}},
		{name: "response already started", err: upstream(http.StatusInternalServerError), retries: 1, setup: func(c *gin.Context) {
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Writer.WriteHeaderNow()
		}, want: PolicyDecision{Action: "stop", Reason: "response_started", Source: "system"}},
		{name: "websocket relay ignores written state", err: upstream(http.StatusInternalServerError), retries: 1, setup: func(c *gin.Context) {
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime", nil)
			c.Request.Header.Set("Connection", "Upgrade")
			c.Request.Header.Set("Upgrade", "websocket")
			c.Writer.WriteHeaderNow()
		}, want: PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}},
		{name: "channel error after response started", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), retries: 1, setup: func(c *gin.Context) {
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Writer.WriteHeaderNow()
		}, want: PolicyDecision{Action: "stop", Reason: "response_started", Source: "system"}},
		{name: "max total attempts reached", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), retries: 1, setup: func(c *gin.Context) {
			common.MaxTotalAttempts = 2
			RequestPolicy(c).Attempts = 2
		}, want: PolicyDecision{Action: "stop", Reason: "max_total_attempts", Source: "global"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.setup != nil {
				tc.setup(c)
			}
			decision := DecideRelayRetry(c, tc.err, tc.retries)
			assert.Equal(t, tc.want, decision)
			assert.Equal(t, tc.want.Action == "retry", ShouldRetryRelayError(c, tc.err, tc.retries))
		})
	}
}

func TestSameChannelRetryBudget(t *testing.T) {
	previous := common.DefaultSameChannelRetryTimes
	t.Cleanup(func() { common.DefaultSameChannelRetryTimes = previous })
	common.DefaultSameChannelRetryTimes = 4

	assert.Equal(t, 4, SameChannelRetryBudget(kitdto.ChannelSettings{}), "an unset channel setting inherits the global default")
	assert.Equal(t, 0, SameChannelRetryBudget(kitdto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(0)}), "an explicit zero disables in-place retries")
	assert.Equal(t, 3, SameChannelRetryBudget(kitdto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(3)}))
	assert.Equal(t, kitdto.MaxSameChannelRetryTimes, SameChannelRetryBudget(kitdto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(99)}), "an oversized budget is clamped")
	assert.Equal(t, 0, SameChannelRetryBudget(kitdto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(-3)}), "a negative budget is clamped")
}

func TestDecideSameChannelRetry(t *testing.T) {
	previousAutoDisable, previousMaxTotalAttempts := common.AutomaticDisableChannelEnabled, common.MaxTotalAttempts
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled, common.MaxTotalAttempts = previousAutoDisable, previousMaxTotalAttempts
	})
	upstream := func(status int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, status)
	}
	retry := PolicyDecision{Action: "retry", Reason: "same_channel_retry", Source: "channel"}
	stop := func(reason string) PolicyDecision {
		return PolicyDecision{Action: "stop", Reason: reason, Source: "channel"}
	}
	for _, tc := range []struct {
		name    string
		err     *types.NewAPIError
		info    *relaycommon.RelayInfo
		attempt int
		budget  int
		setup   func(*gin.Context)
		want    PolicyDecision
	}{
		{name: "server error", err: upstream(http.StatusInternalServerError), budget: 1, want: retry},
		{name: "too many requests", err: upstream(http.StatusTooManyRequests), budget: 1, want: retry},
		{name: "unrecognized status code", err: upstream(0), budget: 1, want: retry},
		{name: "transport failure", err: types.NewError(errors.New("dial"), types.ErrorCodeDoRequestFailed), budget: 1, want: retry},
		{name: "budget zero", err: upstream(http.StatusInternalServerError), budget: 0, want: stop("same_channel_budget_exhausted")},
		{name: "budget spent", err: upstream(http.StatusInternalServerError), attempt: 2, budget: 2, want: stop("same_channel_budget_exhausted")},
		{name: "max total attempts reached", err: upstream(http.StatusInternalServerError), budget: 1, setup: func(c *gin.Context) {
			common.MaxTotalAttempts = 2
			RequestPolicy(c).Attempts = 2
		}, want: PolicyDecision{Action: "stop", Reason: "max_total_attempts", Source: "global"}},
		{name: "status already written", err: upstream(http.StatusInternalServerError), budget: 1, setup: func(c *gin.Context) {
			c.Writer.WriteHeaderNow()
		}, want: stop("response_started")},
		{name: "body byte already written", err: upstream(http.StatusInternalServerError), budget: 1, setup: func(c *gin.Context) {
			_, err := c.Writer.Write([]byte("event: ping\n\n"))
			require.NoError(t, err)
		}, want: stop("response_started")},
		{name: "realtime relay", err: upstream(http.StatusInternalServerError), info: &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime}, budget: 1, want: stop("realtime_unsupported")},
		{name: "skip retry error", err: types.NewErrorWithStatusCode(errors.New("local"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry()), budget: 1, want: stop("non_retryable_error")},
		{name: "channel error", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), budget: 1, want: stop("channel_error")},
		{name: "automatic disable requested", err: upstream(http.StatusUnauthorized), budget: 1, setup: func(*gin.Context) {
			common.AutomaticDisableChannelEnabled = true
		}, want: stop("channel_disable_requested")},
		{name: "single attempt pin", err: upstream(http.StatusInternalServerError), budget: 1, setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
		}, want: stop("pinned_channel")},
		{name: "client error", err: upstream(http.StatusBadRequest), budget: 1, want: stop("status_not_retryable")},
		{name: "success status", err: upstream(http.StatusOK), budget: 1, want: stop("status_not_retryable")},
		{name: "always skipped status", err: upstream(http.StatusGatewayTimeout), budget: 1, want: stop("status_not_retryable")},
		{name: "request cancelled", err: upstream(http.StatusInternalServerError), budget: 1, setup: func(c *gin.Context) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
		}, want: stop("request_cancelled")},
		{name: "no error", budget: 1, want: PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			if tc.setup != nil {
				tc.setup(c)
			}
			assert.Equal(t, tc.want, DecideSameChannelRetry(c, tc.info, tc.err, tc.attempt, tc.budget))
		})
	}
}

func TestRequestPolicyEventsReachLogAdminInfo(t *testing.T) {
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisable })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("auto_ban", true)
	c.Set("channel_id", 7)
	state := RequestPolicy(c)
	state.BeginAttempt(&model.Channel{Id: 7}, "default")
	apiErr := types.NewOpenAIError(errors.New("invalid credential"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)
	RecordPolicyFailure(c, 7, apiErr, DecideRelayRetry(c, apiErr, 0))

	failed := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, failed)
	events, ok := failed.Snapshot()["admin_info"].(map[string]any)["request_policy"].([]PolicyEvent)
	require.True(t, ok, "a failed relay exposes its decision events to administrators")
	require.Len(t, events, 3)
	assert.Equal(t, PolicyDecision{Action: "attempt", Reason: "channel_selected", Source: "routing"}, events[0].Decision)
	assert.Equal(t, "default", events[0].Group)
	assert.Equal(t, PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}, events[1].Decision)
	assert.Equal(t, http.StatusUnauthorized, events[1].Status)
	assert.Equal(t, PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}, events[2].Decision)
	assert.Equal(t, "channel_disable_requested", events[2].Health, "the health entry follows the automatic disable rules")
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	RecordPolicyFailure(c, 7, apiErr, DecideRelayRetry(c, apiErr, 0))
	assert.Equal(t, "key_disable_requested", state.Events()[4].Health)

	state.BeginAttempt(&model.Channel{Id: 8}, "default")
	c.Set("channel_id", 8)
	MarkRequestPolicySuccess(c, nil)
	MarkRequestPolicySuccess(c, nil)
	succeeded := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, succeeded)
	events, ok = succeeded.Snapshot()["admin_info"].(map[string]any)["request_policy"].([]PolicyEvent)
	require.True(t, ok, "a successful relay exposes its decision events to administrators")
	require.Len(t, events, 7, "the outcome is recorded once")
	assert.Equal(t, PolicyDecision{Action: "success", Reason: "request_completed", Source: "upstream"}, events[6].Decision)
	assert.Equal(t, 8, events[6].ChannelID)
	assert.Equal(t, 2, events[6].Attempt)
	assert.True(t, state.Successful)

	untouched, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := model.NewLogOther()
	AppendRelayLogAdminInfo(untouched, nil, other)
	assert.NotContains(t, other.Snapshot()["admin_info"], "request_policy", "requests without decisions do not carry an empty record")
}

// A token model redirect is the token owner's own configuration, so the
// client-facing model name is public in their consume and error logs. The
// active routing profile and route preset are recorded by name only.
func TestAppendRelayLogAdminInfoExposesTokenMappedClientModel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyTokenModelMappingClientModel, "claude-opus-4-8")
	common.SetContextKey(c, constant.ContextKeyTokenProfile, "dsv4f")
	common.SetContextKey(c, constant.ContextKeyTokenRoutePreset, "normal")
	mapped := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, mapped)
	snapshot := mapped.Snapshot()
	assert.Equal(t, "claude-opus-4-8", snapshot["client_model"])
	assert.Equal(t, "dsv4f", snapshot["token_profile"])
	assert.Equal(t, "normal", snapshot["route_preset"])
	adminInfo, ok := snapshot["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, adminInfo, "client_model", "the client model is displayed to the token owner, not admin-only")
	assert.NotContains(t, adminInfo, "token_profile")
	assert.NotContains(t, adminInfo, "route_preset")

	unmapped, _ := gin.CreateTestContext(httptest.NewRecorder())
	unmappedOther := model.NewLogOther()
	AppendRelayLogAdminInfo(unmapped, nil, unmappedOther)
	unmappedSnapshot := unmappedOther.Snapshot()
	assert.NotContains(t, unmappedSnapshot, "client_model")
	assert.NotContains(t, unmappedSnapshot, "token_profile")
	assert.NotContains(t, unmappedSnapshot, "route_preset")
}

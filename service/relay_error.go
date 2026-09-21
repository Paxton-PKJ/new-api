package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	// The total attempt fuse is a hard cap, so it precedes the channel errors
	// that would otherwise retry regardless of any budget.
	if common.MaxTotalAttempts > 0 && RequestPolicy(c).Attempts >= common.MaxTotalAttempts {
		return PolicyDecision{Action: "stop", Reason: "max_total_attempts", Source: "global"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

// SameChannelRetryBudget resolves the in-place retry budget for the selected
// channel: the channel setting when present, otherwise the global default.
func SameChannelRetryBudget(setting kitdto.ChannelSettings) int {
	budget := common.DefaultSameChannelRetryTimes
	if setting.SameChannelRetryTimes != nil {
		budget = *setting.SameChannelRetryTimes
	}
	return min(max(budget, 0), kitdto.MaxSameChannelRetryTimes)
}

// DecideSameChannelRetry decides whether the failed attempt may be repeated on
// the same channel without touching channel selection state. A stop decision is
// not recorded: the caller falls back to DecideRelayRetry with the same error.
func DecideSameChannelRetry(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError, attempt, budget int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if attempt >= budget {
		return PolicyDecision{Action: "stop", Reason: "same_channel_budget_exhausted", Source: "channel"}
	}
	if common.MaxTotalAttempts > 0 && RequestPolicy(c).Attempts >= common.MaxTotalAttempts {
		return PolicyDecision{Action: "stop", Reason: "max_total_attempts", Source: "global"}
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		return PolicyDecision{Action: "stop", Reason: "request_cancelled", Source: "channel"}
	}
	// Any byte already handed to the client (headers, SSE, ping, hijack) makes
	// a second upstream call unobservable, so only untouched responses retry.
	if c.Writer.Written() {
		return PolicyDecision{Action: "stop", Reason: "response_started", Source: "channel"}
	}
	if info != nil && info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return PolicyDecision{Action: "stop", Reason: "realtime_unsupported", Source: "channel"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "channel"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "stop", Reason: "channel_error", Source: "channel"}
	}
	if ShouldDisableChannel(err) {
		return PolicyDecision{Action: "stop", Reason: "channel_disable_requested", Source: "channel"}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel"}
	}
	if code := err.StatusCode; code >= 100 && code <= 599 {
		retryable := !(code >= 200 && code < 300) &&
			!operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) &&
			operation_setting.ShouldRetryByStatusCode(code)
		if !retryable {
			return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "channel"}
		}
	}
	return PolicyDecision{Action: "retry", Reason: "same_channel_retry", Source: "channel"}
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}

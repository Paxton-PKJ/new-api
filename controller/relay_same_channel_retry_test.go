package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mock upstream routes by base-url path prefix, so a recorded call names the
// channel that served it. In-place retries repeat the prefix, which is exactly
// what the sequences below assert.
const tokenMappingSSEPath = "/anthropic"

// upstreamSequence returns one letter per upstream call: F for the fengwind
// channel, A for agent, Q for qq, and S for the native Claude SSE channel.
func upstreamSequence(t *testing.T, fixture *tokenModelMappingFixture) []string {
	t.Helper()
	labels := map[string]string{
		tokenMappingFengwindPath: "F",
		tokenMappingAgentPath:    "A",
		tokenMappingQQPath:       "Q",
		tokenMappingSSEPath:      "S",
	}
	sequence := make([]string, 0)
	for _, call := range fixture.upstream.takeCalls() {
		label := ""
		for prefix, name := range labels {
			if strings.HasPrefix(call.Path, prefix+"/") {
				label = name
			}
		}
		require.NotEmpty(t, label, "unexpected upstream call: %s", call.Path)
		sequence = append(sequence, label)
	}
	return sequence
}

// policyDecisions reads the request policy decision events out of a log's
// admin_info, including the ones only the consume and error logs carry.
func policyDecisions(t *testing.T, other map[string]any) []map[string]any {
	t.Helper()
	events, ok := logAdminInfo(t, other)["request_policy"].([]any)
	require.True(t, ok, "log must carry request policy events: %v", other)
	decisions := make([]map[string]any, 0, len(events))
	for _, event := range events {
		entry, ok := event.(map[string]any)
		require.True(t, ok)
		decision, ok := entry["decision"].(map[string]any)
		require.True(t, ok)
		decisions = append(decisions, decision)
	}
	return decisions
}

func policyReasons(decisions []map[string]any) []string {
	reasons := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		reason, _ := decision["reason"].(string)
		reasons = append(reasons, reason)
	}
	return reasons
}

func lastErrorLog(t *testing.T, token *model.Token) model.Log {
	t.Helper()
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", token.Id, model.LogTypeError).
		Order("id desc").First(&log).Error)
	return log
}

// sseEvent renders one Claude streaming event for the mock upstream.
func sseEvent(eventType, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
}

// setGlobalAttemptLimits pins the two global attempt budgets for one subtest.
func setGlobalAttemptLimits(t *testing.T, defaultSameChannelRetryTimes, maxTotalAttempts int) {
	t.Helper()
	previousDefault, previousMax := common.DefaultSameChannelRetryTimes, common.MaxTotalAttempts
	common.DefaultSameChannelRetryTimes, common.MaxTotalAttempts = defaultSameChannelRetryTimes, maxTotalAttempts
	t.Cleanup(func() {
		common.DefaultSameChannelRetryTimes, common.MaxTotalAttempts = previousDefault, previousMax
	})
}

// insertSSEStreamChannel adds a native Claude channel that answers with the
// mock's SSE scripts, on its own group and token so the streaming cases never
// share a group with the OpenAI test channels. The channel allows two in-place
// retries and the token is returned for the client request.
func (f *tokenModelMappingFixture) insertSSEStreamChannel(t *testing.T) *model.Token {
	t.Helper()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":1,"rt-dsv4f-fengwind":1,"rt-dsv4f-agent":1,"rt-dsv4f-qq":1,"rt-misc":1,"rt-anthropic":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"default":"Default","auto":"Auto","rt-dsv4f-fengwind":"F","rt-dsv4f-agent":"A","rt-dsv4f-qq":"Q","rt-misc":"M","rt-anthropic":"SSE"}`))
	channel := f.insertChannelOfType(t, constant.ChannelTypeAnthropic, "ANTHROPIC-SSE", "rt-anthropic", tokenMappingLogicalModel, tokenMappingSSEPath, nil)
	f.setChannelSetting(t, channel, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(2)})
	return f.insertToken(t, "cc-sse", "ccssemock", "rt-anthropic", nil, false, "")
}

const sseMessagesBody = `{"model":"dsv4f","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`

// sameChannelRecoversInPlace is the shared body of the plain and Redis-backed
// variants: two failures then a success, all on the first selected channel.
func sameChannelRecoversInPlace(t *testing.T, fixture *tokenModelMappingFixture) {
	t.Helper()
	fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(2)})
	fixture.upstream.failFirst(tokenMappingFengwindPath, 2)

	response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	assert.Equal(t, []string{"F", "F", "F"}, upstreamSequence(t, fixture), "the whole request stays on the selected channel")
	log := lastConsumeLog(t, fixture.ccDsv4f)
	assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
	other := logOther(t, log)
	useChannel, ok := logAdminInfo(t, other)["use_channel"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{
		strconv.Itoa(fixture.channels.fengwind.Id),
		strconv.Itoa(fixture.channels.fengwind.Id),
		strconv.Itoa(fixture.channels.fengwind.Id),
	}, useChannel)
	reasons := policyReasons(policyDecisions(t, other))
	inPlaceRetries := 0
	for _, reason := range reasons {
		if reason == "same_channel_retry" {
			inPlaceRetries++
		}
	}
	assert.Equal(t, 2, inPlaceRetries, "each in-place retry records its decision: %v", reasons)
	assert.Equal(t, "request_completed", reasons[len(reasons)-1])
}

// TestSameChannelRetryEndToEnd covers the in-place retry budget and the total
// attempt fuse through the real HTTP relay: recovery on one channel, falling
// back to the existing group switching, inheritance and override of the global
// default, the fuse, and the streaming cases where bytes already reached the
// client.
func TestSameChannelRetryEndToEnd(t *testing.T) {
	t.Run("S-I1_in_place_retry_recovers_on_the_same_channel", func(t *testing.T) {
		sameChannelRecoversInPlace(t, newTokenModelMappingFixture(t))
	})

	t.Run("S-I2_budget_exhausted_falls_back_to_group_switching", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(1)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		// The in-place retry does not advance the retry index: the fengwind group
		// still gets its two existing selections before the Auto group order moves
		// on, and each selection retries once in place.
		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F", "F", "A"}, upstreamSequence(t, fixture))
	})

	t.Run("S-I3_defaults_keep_the_existing_sequence", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "A"}, upstreamSequence(t, fixture), "an unset budget repeats the pre-feature sequence")
	})

	t.Run("S-I4_global_default_inherited_and_channel_zero_overrides", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		setGlobalAttemptLimits(t, 1, 0)
		fixture.setChannelSetting(t, fixture.channels.agent, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(0)})
		fixture.upstream.failPath(tokenMappingFengwindPath)
		fixture.upstream.failPath(tokenMappingAgentPath)

		// fengwind inherits one in-place retry and agent disables it, so every
		// fengwind selection is attempted twice and every agent selection once.
		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F", "F", "A", "A", "Q"}, upstreamSequence(t, fixture))
	})

	t.Run("S-I5_max_total_attempts_fuse", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		setGlobalAttemptLimits(t, 0, 3)
		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(5)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, fixture.ccDsv4f, tokenMappingClientModel)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F"}, upstreamSequence(t, fixture), "the fuse stops the request at the global attempt cap")

		decisions := policyDecisions(t, logOther(t, lastErrorLog(t, fixture.ccDsv4f)))
		require.NotEmpty(t, decisions)
		assert.Equal(t, map[string]any{"action": "stop", "reason": "max_total_attempts", "source": "global"}, decisions[len(decisions)-1])
	})

	t.Run("S-I6_written_stream_is_not_retried_in_place", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
		token := fixture.insertSSEStreamChannel(t)
		fixture.upstream.sseScript(tokenMappingSSEPath, []string{
			sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"dsv4f","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`),
			sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`),
			sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`),
		})

		response := fixture.postMessagesBody(t, token, sseMessagesBody)
		assert.Contains(t, response.Body.String(), "hello", "the client keeps the bytes the failed attempt already sent")
		assert.Equal(t, []string{"S", "S"}, upstreamSequence(t, fixture),
			"the in-place budget is unused once bytes were written; the outer retry still repeats the channel")

		// The first attempt already wrote message_start and a delta, so the failed
		// stream is never repeated in place: the recorded decisions stay with the
		// existing retry rules even though the in-place budget allows two attempts.
		reasons := policyReasons(policyDecisions(t, logOther(t, lastErrorLog(t, token))))
		assert.NotContains(t, reasons, "same_channel_retry", "a started response never retries in place: %v", reasons)
	})

	t.Run("S-I7_first_event_error_is_retried_in_place", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := fixture.insertSSEStreamChannel(t)
		fixture.upstream.sseScript(tokenMappingSSEPath,
			[]string{sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`)},
			[]string{
				sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"dsv4f","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`),
				sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"recovered"}}`),
				sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`),
				sseEvent("message_stop", `{"type":"message_stop"}`),
			})

		response := fixture.postMessagesBody(t, token, sseMessagesBody)
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, []string{"S", "S"}, upstreamSequence(t, fixture), "an error as the first event never reached the client")
		body := response.Body.String()
		assert.Contains(t, body, "recovered", "the client receives the retried stream")
		assert.Equal(t, 1, strings.Count(body, "event: message_start"), "the abandoned attempt contributed no bytes: %s", body)
		log := lastConsumeLog(t, token)
		assert.Contains(t, policyReasons(policyDecisions(t, logOther(t, log))), "same_channel_retry")
	})

	t.Run("Redis_enabled_variant", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		redisServer := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		common.RedisEnabled, common.RDB = true, client

		sameChannelRecoversInPlace(t, fixture)
	})
}

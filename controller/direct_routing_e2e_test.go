package controller

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A direct route preset selects channels by their stable route identity. The
// virtual group carrying that identity lives only in the request context:
// nothing is written to abilities, and billing, logs and group statistics keep
// seeing the token owner's user group.
const (
	directRouteProfile          = "direct"
	directRoutePresetAll        = "all"
	directRoutePresetAgentFirst = "agent-first"
	directRoutePresetQQOnly     = "qq-only"
	directRoutePresetStaleFirst = "stale-first"
	// directRouteStaleKey is a well-formed identity that matches no channel, the
	// state a deleted channel leaves behind in a stored preset.
	directRouteStaleKey = "ch_deletedChannel00"
)

func routeKeyOf(channel *model.Channel) string {
	key := channel.GetRouteKey()
	if !model.IsValidChannelRouteKey(key) {
		panic("fixture channel has no valid route key")
	}
	return key
}

// newDirectRouteFixture starts the shared relay fixture with the feature switch
// on and an administrator as the token owner: a direct route preset only applies
// to administrators.
func newDirectRouteFixture(t *testing.T) *tokenModelMappingFixture {
	t.Helper()
	fixture := newTokenModelMappingFixture(t)
	previousFlag := common.EnableDirectChannelRouting
	common.EnableDirectChannelRouting = true
	t.Cleanup(func() { common.EnableDirectChannelRouting = previousFlag })
	require.NoError(t, model.DB.Model(fixture.user).Update("role", common.RoleAdminUser).Error)
	return fixture
}

// directRouteProfileDocument is the routing configuration most cases run on: the
// active profile redirects the Claude client model onto dsv4f, and its presets
// select channels by route key in four orders. staleKey stands in for a channel
// that is gone by the time the preset runs.
func directRouteProfileDocument(fixture *tokenModelMappingFixture, staleKey string) *model.TokenProfileConfig {
	fengwind, agent, qq := routeKeyOf(fixture.channels.fengwind), routeKeyOf(fixture.channels.agent), routeKeyOf(fixture.channels.qq)
	return &model.TokenProfileConfig{
		ActiveProfile: directRouteProfile,
		Profiles: []model.TokenProfile{{
			Name:              directRouteProfile,
			ModelMapping:      map[string]string{tokenMappingClientModel: tokenMappingLogicalModel},
			ActiveRoutePreset: directRoutePresetAll,
			RoutePresets: []model.TokenRoutePreset{
				{Name: directRoutePresetAll, RouteKeys: []string{fengwind, agent, qq}, CrossGroupRetry: true},
				{Name: directRoutePresetAgentFirst, RouteKeys: []string{agent, fengwind, qq}, CrossGroupRetry: true},
				{Name: directRoutePresetQQOnly, RouteKeys: []string{qq}},
				{Name: directRoutePresetStaleFirst, RouteKeys: []string{staleKey, agent, qq}, CrossGroupRetry: true},
			},
		}},
	}
}

// insertDirectRouteToken inserts the auto-group token every case runs on: it has
// no auto groups of its own, so everything it routes comes from the active direct
// preset.
func insertDirectRouteToken(t *testing.T, fixture *tokenModelMappingFixture, staleKey string) *model.Token {
	t.Helper()
	token := &model.Token{
		UserId: fixture.user.Id, Name: "direct-route", Key: "directrouting", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, Group: "auto",
	}
	require.NoError(t, token.SetProfileConfig(directRouteProfileDocument(fixture, staleKey)))
	require.NoError(t, token.Insert())
	return token
}

// storeDirectRoutePreset replaces the token's profile document with a single
// preset over the given identities, the way a dashboard save does.
func storeDirectRoutePreset(t *testing.T, token *model.Token, presetName string, routeKeys ...string) {
	t.Helper()
	require.NoError(t, token.SetProfileConfig(&model.TokenProfileConfig{
		ActiveProfile: directRouteProfile,
		Profiles: []model.TokenProfile{{
			Name:              directRouteProfile,
			ModelMapping:      map[string]string{tokenMappingClientModel: tokenMappingLogicalModel},
			ActiveRoutePreset: presetName,
			RoutePresets:      []model.TokenRoutePreset{{Name: presetName, RouteKeys: routeKeys, CrossGroupRetry: true}},
		}},
	}))
	require.NoError(t, token.Update())
}

// switchDirectRoutePreset flips the active profile and route preset through the
// real management API, which requires an administrator for a direct preset.
func switchDirectRoutePreset(t *testing.T, fixture *tokenModelMappingFixture, token *model.Token, activeProfile, activeRoutePreset *string) tokenAPIResponse {
	t.Helper()
	body := map[string]any{"id": token.Id}
	if activeProfile != nil {
		body["active_profile"] = *activeProfile
	}
	if activeRoutePreset != nil {
		body["active_route_preset"] = *activeRoutePreset
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token/?profile_only=true", body, fixture.user.Id)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	ctx.Set("role", common.RoleAdminUser)
	UpdateToken(ctx)
	return decodeAPIResponse(t, recorder)
}

// assertNoVirtualGroupLeaks keeps the one hard promise of direct routing: the
// virtual group name reaches no persisted field of the log.
func assertNoVirtualGroupLeaks(t *testing.T, log model.Log) {
	t.Helper()
	payload, err := common.Marshal(log)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), model.DirectRouteGroupPrefix)
}

// insertThrowawayChannel inserts a channel and deletes it again, returning the
// identity a preset saved in between would still reference.
func insertThrowawayChannel(t *testing.T, fixture *tokenModelMappingFixture) string {
	t.Helper()
	channel := fixture.insertChannel(t, "THROWAWAY", "rt-dsv4f-fengwind", tokenMappingLogicalModel, tokenMappingMiscPath, nil)
	key := routeKeyOf(channel)
	require.NoError(t, channel.Delete())
	return key
}

// insertDirectSSEChannel adds a native Claude channel answering with the mock's
// SSE scripts. It carries no group of its own: a direct preset selects it by
// route identity, so no group registration is involved.
func insertDirectSSEChannel(t *testing.T, fixture *tokenModelMappingFixture, name string) *model.Channel {
	t.Helper()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	channel := fixture.insertChannelOfType(t, constant.ChannelTypeAnthropic, name, "rt-anthropic", tokenMappingLogicalModel, tokenMappingSSEPath, nil)
	fixture.setChannelSetting(t, channel, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(0)})
	return channel
}

// TestDirectChannelRoutingEndToEnd covers route presets that select channels by
// their stable route identity through the real HTTP relay: the preset order, the
// retry budgets and fuses, the interaction with the token model redirect, the
// model catalog, stale references, the memory-cache and Redis paths, and the
// administrator-plus-switch gate.
func TestDirectChannelRoutingEndToEnd(t *testing.T) {
	t.Run("P1_preset_order_drives_selection", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "A"}, upstreamSequence(t, fixture),
			"the preset retries the fengwind route once, then moves on to the agent route")

		log := lastConsumeLog(t, token)
		assert.Equal(t, "default", log.Group, "direct routing bills the token owner's user group")
		other := logOther(t, log)
		assert.Equal(t, directRoutePresetAll, other["route_preset"])
		adminInfo := logAdminInfo(t, other)
		assert.Equal(t, []any{
			strconv.Itoa(fixture.channels.fengwind.Id),
			strconv.Itoa(fixture.channels.fengwind.Id),
			strconv.Itoa(fixture.channels.agent.Id),
		}, adminInfo["use_channel"])
		assert.Equal(t, routeKeyOf(fixture.channels.agent), adminInfo["route_key"],
			"the route key follows the last selection, like the logged auto group does")
		assertNoVirtualGroupLeaks(t, log)
	})

	t.Run("P2_preset_switch_changes_the_route", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)

		switched := switchDirectRoutePreset(t, fixture, token, nil, common.GetPointer(directRoutePresetAgentFirst))
		require.True(t, switched.Success, switched.Message)
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"A"}, upstreamSequence(t, fixture), "the preset puts the agent route first")
		assert.Equal(t, directRoutePresetAgentFirst, logOther(t, lastConsumeLog(t, token))["route_preset"])

		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
		switched = switchDirectRoutePreset(t, fixture, token, nil, common.GetPointer(directRoutePresetQQOnly))
		require.True(t, switched.Success, switched.Message)
		fixture.upstream.failPath(tokenMappingQQPath)

		// The single-entry preset disables cross-group retry, so the route is
		// attempted twice and then the request fails.
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, []string{"Q", "Q"}, upstreamSequence(t, fixture))
		assert.Equal(t, directRoutePresetQQOnly, logOther(t, lastErrorLog(t, token))["route_preset"])
	})

	t.Run("P3_model_redirect_applies_to_the_direct_route", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)

		// The client names the Claude model; the active profile redirects it to
		// dsv4f, which is what the preset's channels serve.
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))

		log := lastConsumeLog(t, token)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		other := logOther(t, log)
		assert.Equal(t, tokenMappingClientModel, other["client_model"])
		assert.Equal(t, directRoutePresetAll, other["route_preset"])
		assert.Equal(t, routeKeyOf(fixture.channels.fengwind), logAdminInfo(t, other)["route_key"],
			"a request that never retries records the route that served it")
		assertNoVirtualGroupLeaks(t, log)
	})

	t.Run("P4_stale_reference_is_skipped", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, insertThrowawayChannel(t, fixture))

		switched := switchDirectRoutePreset(t, fixture, token, nil, common.GetPointer(directRoutePresetStaleFirst))
		require.True(t, switched.Success, switched.Message)
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"A"}, upstreamSequence(t, fixture),
			"the preset entry whose channel is gone resolves to nothing and the next one serves the request")

		log := lastConsumeLog(t, token)
		assert.Equal(t, "default", log.Group)
		assert.Equal(t, routeKeyOf(fixture.channels.agent), logAdminInfo(t, logOther(t, log))["route_key"])
		assertNoVirtualGroupLeaks(t, log)
	})

	t.Run("P5_rename_and_new_model", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)

		// The route identity is independent of the channel name.
		require.NoError(t, model.DB.Model(fixture.channels.fengwind).Update("name", "FENGWIND-RENAMED").Error)
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))

		// A model added to the channel shows up in the token's catalog, and the
		// same preset serves it.
		require.NoError(t, model.DB.Model(fixture.channels.fengwind).Update("models", tokenMappingLogicalModel+",opus5").Error)
		assert.Equal(t, []string{tokenMappingLogicalModel, "opus5"}, fixture.getModels(t, token))

		response = fixture.postMessages(t, token, "opus5")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingFengwindPath+"/"), calls[0].Path)
		assert.Equal(t, "opus5", calls[0].Model)
	})

	t.Run("P6_in_place_retry_inside_the_preset", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		setGlobalAttemptLimits(t, 0, 0)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(1)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F", "F", "A"}, upstreamSequence(t, fixture),
			"every fengwind selection retries once in place before the preset moves on")
		reasons := policyReasons(policyDecisions(t, logOther(t, lastConsumeLog(t, token))))
		inPlaceRetries := 0
		for _, reason := range reasons {
			if reason == "same_channel_retry" {
				inPlaceRetries++
			}
		}
		assert.Equal(t, 2, inPlaceRetries, "each fengwind selection records its in-place retry: %v", reasons)
	})

	t.Run("P6b_total_attempt_fuse", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		setGlobalAttemptLimits(t, 0, 3)
		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(5)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F"}, upstreamSequence(t, fixture),
			"the fuse stops the request at the global attempt cap even though the preset allows more")
		decisions := policyDecisions(t, logOther(t, lastErrorLog(t, token)))
		require.NotEmpty(t, decisions)
		assert.Equal(t, map[string]any{"action": "stop", "reason": "max_total_attempts", "source": "global"}, decisions[len(decisions)-1])
	})

	t.Run("P7_streaming_errors_follow_the_preset", func(t *testing.T) {
		t.Run("first_event_error_moves_to_the_next_route", func(t *testing.T) {
			fixture := newDirectRouteFixture(t)
			setGlobalAttemptLimits(t, 0, 0)
			first := insertDirectSSEChannel(t, fixture, "ANTHROPIC-DIRECT-A")
			second := insertDirectSSEChannel(t, fixture, "ANTHROPIC-DIRECT-B")
			token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
			storeDirectRoutePreset(t, token, directRoutePresetAll, routeKeyOf(first), routeKeyOf(second))

			// The mock serves one script per request below the path: the first two
			// attempts fail before any event reaches the client, the third recovers.
			fixture.upstream.sseScript(tokenMappingSSEPath,
				[]string{sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`)},
				[]string{sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`)},
				[]string{
					sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"dsv4f","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`),
					sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"recovered"}}`),
					sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`),
					sseEvent("message_stop", `{"type":"message_stop"}`),
				})

			response := fixture.postMessagesBody(t, token, sseMessagesBody)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, []string{"S", "S", "S"}, upstreamSequence(t, fixture),
				"an error as the first event never reached the client, so the route is retried")
			body := response.Body.String()
			assert.Contains(t, body, "recovered")
			assert.Equal(t, 1, strings.Count(body, "event: message_start"), "abandoned attempts contributed no bytes: %s", body)

			other := logOther(t, lastConsumeLog(t, token))
			assert.Equal(t, directRoutePresetAll, other["route_preset"])
			assert.Equal(t, []any{
				strconv.Itoa(first.Id), strconv.Itoa(first.Id), strconv.Itoa(second.Id),
			}, logAdminInfo(t, other)["use_channel"], "the third attempt moved to the second preset entry")
		})

		t.Run("written_bytes_stop_the_retry", func(t *testing.T) {
			fixture := newDirectRouteFixture(t)
			previousErrorLog := constant.ErrorLogEnabled
			constant.ErrorLogEnabled = true
			t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
			channel := insertDirectSSEChannel(t, fixture, "ANTHROPIC-DIRECT-A")
			token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
			storeDirectRoutePreset(t, token, directRoutePresetAll, routeKeyOf(channel), routeKeyOf(fixture.channels.agent))
			fixture.upstream.sseScript(tokenMappingSSEPath, []string{
				sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"dsv4f","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`),
				sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`),
				sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`),
			})

			response := fixture.postMessagesBody(t, token, sseMessagesBody)
			assert.Contains(t, response.Body.String(), "hello", "the client keeps the bytes the failed attempt already sent")
			assert.Equal(t, []string{"S"}, upstreamSequence(t, fixture),
				"a started response is never repeated, in place or through the preset")
			errorLog := lastErrorLog(t, token)
			decisions := policyDecisions(t, logOther(t, errorLog))
			require.NotEmpty(t, decisions)
			assert.Equal(t, map[string]any{"action": "stop", "reason": "response_started", "source": "system"}, decisions[len(decisions)-1])
			assertNoVirtualGroupLeaks(t, errorLog)
		})
	})

	t.Run("P8_administrator_and_switch_gate", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)

		// Without an administrator owner the whole preset is ignored and the token
		// falls back to its own routing, which has no auto groups at all. It is
		// rejected before any selection or billing, so no log is written either.
		require.NoError(t, model.DB.Model(fixture.user).Update("role", common.RoleCommonUser).Error)
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), model.DirectRouteGroupPrefix)
		assert.Empty(t, fixture.upstream.takeCalls())

		// The feature switch alone gates the preset as well, administrators included.
		require.NoError(t, model.DB.Model(fixture.user).Update("role", common.RoleAdminUser).Error)
		common.EnableDirectChannelRouting = false
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.Empty(t, fixture.upstream.takeCalls())

		// Restoring both gates puts the preset back in effect.
		common.EnableDirectChannelRouting = true
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))
		assert.Equal(t, directRoutePresetAll, logOther(t, lastConsumeLog(t, token))["route_preset"])
	})

	t.Run("P9_memory_cache_path", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, insertThrowawayChannel(t, fixture))
		previousCache := common.MemoryCacheEnabled
		common.MemoryCacheEnabled = true
		t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
		model.InitChannelCache()

		fixture.upstream.failPath(tokenMappingFengwindPath)
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "A"}, upstreamSequence(t, fixture))
		log := lastConsumeLog(t, token)
		assert.Equal(t, "default", log.Group)
		assert.Equal(t, routeKeyOf(fixture.channels.agent), logAdminInfo(t, logOther(t, log))["route_key"])

		switched := switchDirectRoutePreset(t, fixture, token, nil, common.GetPointer(directRoutePresetStaleFirst))
		require.True(t, switched.Success, switched.Message)
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"A"}, upstreamSequence(t, fixture),
			"a cached identity still resolves, and the deleted one is not in the cache at all")
	})

	t.Run("P10_redis_backed_switch_takes_effect_immediately", func(t *testing.T) {
		fixture := newDirectRouteFixture(t)
		token := insertDirectRouteToken(t, fixture, directRouteStaleKey)
		redisServer := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		common.RedisEnabled, common.RDB = true, client

		// Warm the token and user caches with the preset the token starts on.
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))

		switched := switchDirectRoutePreset(t, fixture, token, nil, common.GetPointer(directRoutePresetQQOnly))
		require.True(t, switched.Success, switched.Message)
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"Q"}, upstreamSequence(t, fixture), "the cached token hash must not serve the previous preset")
	})
}

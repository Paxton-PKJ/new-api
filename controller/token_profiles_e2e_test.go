package controller

import (
	"net/http"
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

// The routing configuration every case runs on: one profile keeps the token on
// the three dsv4f groups and offers presets that reorder them, the other moves
// the whole token to the misc group.
const (
	tokenProfileDsv4f     = "dsv4f"
	tokenProfileF51       = "f51"
	tokenPresetNormal     = "normal"
	tokenPresetAgentFirst = "agent-first"
	tokenPresetQQOnly     = "qq-only"
	tokenPresetMisc       = "misc"
)

func profiledTokenProfiles() *model.TokenProfileConfig {
	return &model.TokenProfileConfig{
		Profiles: []model.TokenProfile{
			{
				Name:              tokenProfileDsv4f,
				ModelMapping:      map[string]string{tokenMappingClientModel: tokenMappingLogicalModel},
				ModelLimits:       []string{tokenMappingLogicalModel},
				ActiveRoutePreset: tokenPresetNormal,
				RoutePresets: []model.TokenRoutePreset{
					{
						Name:            tokenPresetNormal,
						AutoGroups:      []string{"rt-dsv4f-fengwind", "rt-dsv4f-agent", "rt-dsv4f-qq"},
						CrossGroupRetry: true,
					},
					{
						Name:            tokenPresetAgentFirst,
						AutoGroups:      []string{"rt-dsv4f-agent", "rt-dsv4f-fengwind", "rt-dsv4f-qq"},
						CrossGroupRetry: true,
					},
					{
						Name:       tokenPresetQQOnly,
						AutoGroups: []string{"rt-dsv4f-qq"},
					},
				},
			},
			{
				Name:              tokenProfileF51,
				ModelMapping:      map[string]string{tokenMappingClientModel: "f5.1"},
				ModelLimits:       []string{"f5.1"},
				ActiveRoutePreset: tokenPresetMisc,
				RoutePresets: []model.TokenRoutePreset{
					{Name: tokenPresetMisc, AutoGroups: []string{"rt-misc"}},
				},
			},
		},
	}
}

// storeTokenProfiles persists the shared profile document on an existing token,
// with activeProfile as the active profile.
func storeTokenProfiles(t *testing.T, token *model.Token, activeProfile string) {
	t.Helper()
	config := profiledTokenProfiles()
	config.ActiveProfile = activeProfile
	require.NoError(t, token.SetProfileConfig(config))
	require.NoError(t, token.Update())
}

// insertProfiledToken adds an auto-group token whose own fields carry neither a
// model redirect nor model limits, so everything the relay does with it comes
// from the active profile.
func insertProfiledToken(t *testing.T, fixture *tokenModelMappingFixture) *model.Token {
	t.Helper()
	token := fixture.insertToken(t, "profiled", "profiledtoken", "auto", nil, false, "")
	storeTokenProfiles(t, token, tokenProfileDsv4f)
	return token
}

// switchProfile flips the active profile and route preset through the real
// management API, the way the dashboard does. No cache is touched by hand: a
// switch has to take effect through Token.Update alone.
func switchProfile(t *testing.T, fixture *tokenModelMappingFixture, token *model.Token, activeProfile, activeRoutePreset *string) tokenAPIResponse {
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
	UpdateToken(ctx)
	return decodeAPIResponse(t, recorder)
}

// TestRoutingProfilesEndToEnd covers the whole point of routing profiles
// through the real HTTP relay: one key and one client model stay fixed while the
// active profile and route preset decide which logical model the token maps to
// and in which order the auto groups are tried, including the in-place retry
// budget, the total attempt fuse, and immediate effect with the token cache on.
func TestRoutingProfilesEndToEnd(t *testing.T) {
	t.Run("L1_profile_switch_changes_logical_model_and_upstream", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingFengwindPath+"/"), calls[0].Path)
		assert.Equal(t, tokenMappingFengwindModel, calls[0].Model)

		log := lastConsumeLog(t, token)
		assert.Equal(t, tokenMappingLogicalModel, log.ModelName)
		other := logOther(t, log)
		assert.Equal(t, tokenMappingClientModel, other["client_model"])
		assert.Equal(t, tokenProfileDsv4f, other["token_profile"])
		assert.Equal(t, tokenPresetNormal, other["route_preset"])
		adminInfo := logAdminInfo(t, other)
		assert.NotContains(t, adminInfo, "client_model")
		assert.NotContains(t, adminInfo, "token_profile")
		assert.NotContains(t, adminInfo, "route_preset")

		switched := switchProfile(t, fixture, token, common.GetPointer(tokenProfileF51), nil)
		require.True(t, switched.Success, switched.Message)
		var payload struct {
			Profiles *model.TokenProfileConfig `json:"profiles"`
		}
		require.NoError(t, common.Unmarshal(switched.Data, &payload))
		require.NotNil(t, payload.Profiles)
		assert.Equal(t, tokenProfileF51, payload.Profiles.ActiveProfile)

		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls = fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingMiscPath+"/"), calls[0].Path)
		assert.Equal(t, "f5.1", calls[0].Model)

		log = lastConsumeLog(t, token)
		assert.Equal(t, "f5.1", log.ModelName)
		other = logOther(t, log)
		assert.Equal(t, tokenMappingClientModel, other["client_model"])
		assert.Equal(t, tokenProfileF51, other["token_profile"])
		assert.Equal(t, tokenPresetMisc, other["route_preset"])
	})

	t.Run("L2_preset_switch_changes_group_order", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)
		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })

		switched := switchProfile(t, fixture, token, nil, common.GetPointer(tokenPresetAgentFirst))
		require.True(t, switched.Success, switched.Message)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"A"}, upstreamSequence(t, fixture), "the preset puts the agent group first")
		assert.Equal(t, tokenPresetAgentFirst, logOther(t, lastConsumeLog(t, token))["route_preset"])

		switched = switchProfile(t, fixture, token, nil, common.GetPointer(tokenPresetQQOnly))
		require.True(t, switched.Success, switched.Message)
		fixture.upstream.failPath(tokenMappingQQPath)

		// The single-group preset disables cross-group retry, so the group's own
		// two selections are all the request gets before it gives up.
		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, []string{"Q", "Q"}, upstreamSequence(t, fixture))

		errorOther := logOther(t, lastErrorLog(t, token))
		assert.Equal(t, tokenProfileDsv4f, errorOther["token_profile"])
		assert.Equal(t, tokenPresetQQOnly, errorOther["route_preset"])
	})

	t.Run("L3_cleared_active_profile_restores_base_behaviour", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)

		switched := switchProfile(t, fixture, token, common.GetPointer(""), nil)
		require.True(t, switched.Success, switched.Message)

		// Without the profile redirect the client model is not a model any channel
		// of the token's groups serves.
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), tokenMappingClientModel)
		assert.Empty(t, fixture.upstream.takeCalls())

		response = fixture.postMessages(t, token, tokenMappingLogicalModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))

		other := logOther(t, lastConsumeLog(t, token))
		assert.NotContains(t, other, "token_profile")
		assert.NotContains(t, other, "route_preset")
		assert.NotContains(t, other, "client_model")
	})

	t.Run("L4_profile_model_limits_and_catalog", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)

		// The profile limits cover the logical model only, so a model the client
		// could name directly is rejected even though it is unmapped.
		response := fixture.postMessages(t, token, tokenMappingLogicalModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))

		response = fixture.postMessages(t, token, "f5.1")
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "f5.1")

		assert.Equal(t, []string{tokenMappingLogicalModel}, fixture.getModels(t, token))

		switched := switchProfile(t, fixture, token, common.GetPointer(tokenProfileF51), nil)
		require.True(t, switched.Success, switched.Message)
		assert.Equal(t, []string{"f5.1"}, fixture.getModels(t, token))
	})

	t.Run("L5_non_auto_group_token_ignores_route_presets", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		// The model layer accepts profiles on any token; only the management API
		// refuses route presets outside an auto group.
		token := fixture.insertToken(t, "misc-profiled", "miscprofiled", "rt-misc", nil, false, "")
		storeTokenProfiles(t, token, tokenProfileF51)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingMiscPath+"/"), calls[0].Path)
		assert.Equal(t, "f5.1", calls[0].Model)

		other := logOther(t, lastConsumeLog(t, token))
		assert.Equal(t, tokenProfileF51, other["token_profile"])
		assert.NotContains(t, other, "route_preset", "a fixed-group token has no auto groups to reorder")
	})

	t.Run("L6_profile_routing_keeps_same_channel_retry", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(1)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F", "F", "A"}, upstreamSequence(t, fixture),
			"every fengwind selection retries once in place before the preset moves on to the agent group")

		other := logOther(t, lastConsumeLog(t, token))
		assert.Equal(t, tokenProfileDsv4f, other["token_profile"])
		assert.Equal(t, tokenPresetNormal, other["route_preset"])
		inPlaceRetries := 0
		reasons := policyReasons(policyDecisions(t, other))
		for _, reason := range reasons {
			if reason == "same_channel_retry" {
				inPlaceRetries++
			}
		}
		assert.Equal(t, 2, inPlaceRetries, "each fengwind selection records its in-place retry: %v", reasons)
	})

	t.Run("L7_profile_routing_respects_the_attempt_fuse", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		setGlobalAttemptLimits(t, 0, 3)
		previousErrorLog := constant.ErrorLogEnabled
		constant.ErrorLogEnabled = true
		t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
		token := insertProfiledToken(t, fixture)
		fixture.setChannelSetting(t, fixture.channels.fengwind, dto.ChannelSettings{SameChannelRetryTimes: common.GetPointer(5)})
		fixture.upstream.failPath(tokenMappingFengwindPath)

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, []string{"F", "F", "F"}, upstreamSequence(t, fixture),
			"the fuse stops the request at the global attempt cap even though the profile allows more")

		other := logOther(t, lastErrorLog(t, token))
		assert.Equal(t, tokenProfileDsv4f, other["token_profile"])
		decisions := policyDecisions(t, other)
		require.NotEmpty(t, decisions)
		assert.Equal(t, map[string]any{"action": "stop", "reason": "max_total_attempts", "source": "global"}, decisions[len(decisions)-1])
	})

	t.Run("L8_redis_backed_switch_takes_effect_immediately", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)
		redisServer := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		common.RedisEnabled, common.RDB = true, client

		// Warm the token hash with the profile the token starts on.
		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls := fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, tokenMappingFengwindModel, calls[0].Model)

		switched := switchProfile(t, fixture, token, common.GetPointer(tokenProfileF51), nil)
		require.True(t, switched.Success, switched.Message)

		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		calls = fixture.upstream.takeCalls()
		require.Len(t, calls, 1)
		assert.True(t, strings.HasPrefix(calls[0].Path, tokenMappingMiscPath+"/"), calls[0].Path)
		assert.Equal(t, "f5.1", calls[0].Model, "the cached token hash must not serve the previous profile")

		switched = switchProfile(t, fixture, token, common.GetPointer(tokenProfileDsv4f), common.GetPointer(tokenPresetAgentFirst))
		require.True(t, switched.Success, switched.Message)

		response = fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"A"}, upstreamSequence(t, fixture))
	})

	t.Run("L9_rejected_switches_leave_the_stored_profiles_alone", func(t *testing.T) {
		fixture := newTokenModelMappingFixture(t)
		token := insertProfiledToken(t, fixture)

		rejected := switchProfile(t, fixture, token, common.GetPointer("nope"), nil)
		assert.False(t, rejected.Success)
		assert.Contains(t, rejected.Message, "nope")

		rejected = switchProfile(t, fixture, token, nil, common.GetPointer("nope"))
		assert.False(t, rejected.Success)
		assert.Contains(t, rejected.Message, "nope")
		assert.Contains(t, rejected.Message, tokenProfileDsv4f, "the preset error names the profile it searched")

		response := fixture.postMessages(t, token, tokenMappingClientModel)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"F"}, upstreamSequence(t, fixture))
	})
}

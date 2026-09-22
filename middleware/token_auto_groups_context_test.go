package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTokenAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestSetupContextForTokenPreservesCustomAutoGroupsOrder(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `["vip","default"]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{"vip", "default"}, value)
}

func TestSetupContextForTokenTreatsStoredEmptyArrayAsInheritance(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `[]`}

	require.NoError(t, SetupContextForToken(ctx, token))
	_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	assert.False(t, ok)
}

func TestSetupContextForTokenMalformedAutoGroupsFailsClosed(t *testing.T) {
	ctx := newTokenAutoGroupsContext()
	token := &model.Token{Id: 1, UserId: 2, AutoGroups: `not-json`}

	require.NoError(t, SetupContextForToken(ctx, token))
	value, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
	require.True(t, ok)
	assert.Equal(t, []string{}, value)
}

// TestSetupContextForTokenAppliesActiveProfile pins the request-time overlay:
// the active profile replaces the token model mapping and limits, its active
// route preset replaces the auto groups, and the applied names land in the
// context as their own keys.
func TestSetupContextForTokenAppliesActiveProfile(t *testing.T) {
	const profiles = `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"},"model_limits":["dsv4f"],"active_route_preset":"normal","route_presets":[{"name":"normal","auto_groups":["fengwind","agent"],"cross_group_retry":true}]}]}`

	newProfiledToken := func(group string) *model.Token {
		return &model.Token{
			Id:                 1,
			UserId:             2,
			Group:              group,
			ModelLimitsEnabled: true,
			ModelLimits:        "base-model",
			ModelMapping:       common.GetPointer(`{"a":"b"}`),
			AutoGroups:         `["base1","base2"]`,
			CrossGroupRetry:    false,
			Profiles:           common.GetPointer(profiles),
		}
	}

	t.Run("auto group token", func(t *testing.T) {
		ctx := newTokenAutoGroupsContext()
		require.NoError(t, SetupContextForToken(ctx, newProfiledToken("auto")))

		mapping, ok := common.GetContextKeyType[map[string]string](ctx, constant.ContextKeyTokenModelMapping)
		require.True(t, ok)
		assert.Equal(t, map[string]string{"claude-opus-4-8": "dsv4f"}, mapping)
		limits, exists := ctx.Get("token_model_limit")
		require.True(t, exists)
		assert.Equal(t, map[string]bool{"dsv4f": true}, limits)
		groups, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
		require.True(t, ok)
		assert.Equal(t, []string{"fengwind", "agent"}, groups)
		assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenCrossGroupRetry))
		assert.Equal(t, "dsv4f", common.GetContextKeyString(ctx, constant.ContextKeyTokenProfile))
		assert.Equal(t, "normal", common.GetContextKeyString(ctx, constant.ContextKeyTokenRoutePreset))
	})

	t.Run("document without active profile", func(t *testing.T) {
		ctx := newTokenAutoGroupsContext()
		token := newProfiledToken("auto")
		token.Profiles = common.GetPointer(`{"profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"}}]}`)

		require.NoError(t, SetupContextForToken(ctx, token))
		mapping, ok := common.GetContextKeyType[map[string]string](ctx, constant.ContextKeyTokenModelMapping)
		require.True(t, ok)
		assert.Equal(t, map[string]string{"a": "b"}, mapping)
		limits, exists := ctx.Get("token_model_limit")
		require.True(t, exists)
		assert.Equal(t, map[string]bool{"base-model": true}, limits)
		groups, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
		require.True(t, ok)
		assert.Equal(t, []string{"base1", "base2"}, groups)
		_, profiled := common.GetContextKey(ctx, constant.ContextKeyTokenProfile)
		assert.False(t, profiled)
		_, preset := common.GetContextKey(ctx, constant.ContextKeyTokenRoutePreset)
		assert.False(t, preset)
	})

	t.Run("direct route preset keeps the auto groups unset", func(t *testing.T) {
		const directProfiles = `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","active_route_preset":"direct","route_presets":[{"name":"direct","route_keys":["ch_0123456789AbCdEf"],"cross_group_retry":true}]}]}`
		ctx := newTokenAutoGroupsContext()
		token := &model.Token{
			Id:              1,
			UserId:          2,
			Group:           "auto",
			CrossGroupRetry: false,
			Profiles:        common.GetPointer(directProfiles),
		}

		require.NoError(t, SetupContextForToken(ctx, token))
		_, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
		assert.False(t, ok, "a direct preset selects channels by route key, not by auto groups")
		assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenCrossGroupRetry))
		assert.Equal(t, "dsv4f", common.GetContextKeyString(ctx, constant.ContextKeyTokenProfile))
		assert.Equal(t, "direct", common.GetContextKeyString(ctx, constant.ContextKeyTokenRoutePreset))
	})

	t.Run("non auto group ignores the route preset", func(t *testing.T) {
		ctx := newTokenAutoGroupsContext()
		require.NoError(t, SetupContextForToken(ctx, newProfiledToken("default")))

		mapping, ok := common.GetContextKeyType[map[string]string](ctx, constant.ContextKeyTokenModelMapping)
		require.True(t, ok)
		assert.Equal(t, map[string]string{"claude-opus-4-8": "dsv4f"}, mapping)
		limits, exists := ctx.Get("token_model_limit")
		require.True(t, exists)
		assert.Equal(t, map[string]bool{"dsv4f": true}, limits)
		groups, ok := common.GetContextKey(ctx, constant.ContextKeyTokenAutoGroups)
		require.True(t, ok)
		assert.Equal(t, []string{"base1", "base2"}, groups)
		assert.False(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenCrossGroupRetry))
		assert.Equal(t, "dsv4f", common.GetContextKeyString(ctx, constant.ContextKeyTokenProfile))
		_, preset := common.GetContextKey(ctx, constant.ContextKeyTokenRoutePreset)
		assert.False(t, preset)
	})
}

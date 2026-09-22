package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureRequestAutoGroupsTest(t *testing.T) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","svip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func newRequestAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestGetRequestAutoGroupsInheritedListIsNotLimited(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default", "svip"}, groups)
}

func TestGetRequestAutoGroupsFiltersBeforeApplyingCurrentLimit(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"revoked", "vip", "default", "svip"})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default"}, groups)
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"))
}

func TestGetRequestAutoGroupsDoesNotFallBackAfterPermissionChange(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Empty(t, groups)
}

// A direct route preset turns its route keys into virtual groups in order. The
// list is the trusted output of SetupContextForToken, so the token auto-group
// limit and the user-facing group visibility never trim it.
func TestGetRequestAutoGroupsPrefersDirectRoutePreset(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	previousFlag := common.EnableDirectChannelRouting
	t.Cleanup(func() { common.EnableDirectChannelRouting = previousFlag })
	common.EnableDirectChannelRouting = true

	// The virtual groups are not user-selectable groups, but they are deliberately
	// present in both registries: the direct preset must not depend on them.
	const routeGroup = "__route_ch_0123456789AbCdEf"
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"default":"Default","vip":"VIP","`+routeGroup+`":"Route"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":1,"vip":1,"`+routeGroup+`":1}`))

	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenRouteChannels,
		[]string{"ch_0123456789AbCdEf", "ch_fedCbA9876543210", "ch_ABCDEF0123456789"})

	assert.Equal(t, []string{routeGroup, "__route_ch_fedCbA9876543210", "__route_ch_ABCDEF0123456789"},
		GetRequestAutoGroups(ctx, "default"), "the preset order survives the per-token auto group limit")
	assert.False(t, IsUserSelectableGroup("default", routeGroup), "a virtual group is never user selectable")
	assert.False(t, IsUserSelectableGroup("default", "auto"))
	assert.True(t, IsUserSelectableGroup("default", "vip"))
}

func TestGetRequestAutoGroupsIgnoresDirectRoutePresetWhenDisabled(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	previousFlag := common.EnableDirectChannelRouting
	t.Cleanup(func() { common.EnableDirectChannelRouting = previousFlag })
	common.EnableDirectChannelRouting = false

	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenRouteChannels, []string{"ch_0123456789AbCdEf"})
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})

	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"),
		"a disabled feature falls back to the token's own auto groups")
}

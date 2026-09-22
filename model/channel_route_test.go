package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetChannelRouteKeys(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
}

func TestGenerateChannelRouteKeyFormat(t *testing.T) {
	for range 8 {
		key, err := GenerateChannelRouteKey()
		require.NoError(t, err)
		assert.True(t, IsValidChannelRouteKey(key), "generated key %q must match the route key pattern", key)
	}

	for _, invalid := range []string{
		"",
		"ch_",
		"0123456789ABCDEF",
		"xk_0123456789ABCDEF",
		"ch_0123456789ABCDE",
		"ch_0123456789ABCDEFG",
		"ch_0123456789ABCDE-",
	} {
		assert.Falsef(t, IsValidChannelRouteKey(invalid), "key %q must be rejected", invalid)
	}
}

func TestChannelCreateAssignsRouteKey(t *testing.T) {
	resetChannelRouteKeys(t)

	generated := Channel{Name: "route-key-generated", Key: "sk-generated"}
	require.NoError(t, DB.Create(&generated).Error)
	require.NotNil(t, generated.RouteKey)
	assert.True(t, IsValidChannelRouteKey(generated.GetRouteKey()))

	preset := "ch_presetRouteKey01"
	preassigned := Channel{Name: "route-key-preset", Key: "sk-preset", RouteKey: &preset}
	require.NoError(t, DB.Create(&preassigned).Error)
	require.NotNil(t, preassigned.RouteKey)
	assert.Equal(t, preset, preassigned.GetRouteKey())
}

func TestChannelRouteKeyIsUnique(t *testing.T) {
	resetChannelRouteKeys(t)

	duplicate := "ch_duplicateKey0001"
	first := Channel{Name: "route-key-unique-a", Key: "sk-a", RouteKey: &duplicate}
	require.NoError(t, DB.Create(&first).Error)

	second := Channel{Name: "route-key-unique-b", Key: "sk-b", RouteKey: &duplicate}
	require.Error(t, DB.Create(&second).Error)
}

func TestBatchInsertChannelsAssignsDistinctRouteKeys(t *testing.T) {
	resetChannelRouteKeys(t)

	channels := []Channel{
		{Name: "batch-route-a", Key: "sk-batch-a", Group: "default", Models: "gpt-4o"},
		{Name: "batch-route-b", Key: "sk-batch-b", Group: "default", Models: "gpt-4o"},
		{Name: "batch-route-c", Key: "sk-batch-c", Group: "default", Models: "gpt-4o"},
	}
	require.NoError(t, BatchInsertChannels(channels))

	var stored []Channel
	require.NoError(t, DB.Order("id ASC").Find(&stored).Error)
	require.Len(t, stored, 3)

	seen := make(map[string]struct{}, len(stored))
	for _, channel := range stored {
		key := channel.GetRouteKey()
		require.Truef(t, IsValidChannelRouteKey(key), "channel %d has invalid route key %q", channel.Id, key)
		_, duplicate := seen[key]
		assert.Falsef(t, duplicate, "route key %q was reused across batch channels", key)
		seen[key] = struct{}{}
	}
}

func TestBackfillChannelRouteKeysIsIdempotent(t *testing.T) {
	resetChannelRouteKeys(t)

	existing := "ch_existingRouteKey"
	require.NoError(t, DB.Exec(
		"INSERT INTO channels (type, `key`, status, name, models, `group`, route_key) VALUES (1, 'sk-null', 1, 'backfill-null', 'gpt-4o', 'default', NULL)").
		Error)
	require.NoError(t, DB.Exec(
		"INSERT INTO channels (type, `key`, status, name, models, `group`, route_key) VALUES (1, 'sk-empty', 1, 'backfill-empty', 'gpt-4o', 'default', '')").
		Error)
	require.NoError(t, DB.Exec(
		"INSERT INTO channels (type, `key`, status, name, models, `group`, route_key) VALUES (1, 'sk-existing', 1, 'backfill-existing', 'gpt-4o', 'default', ?)", existing).
		Error)

	filled, err := BackfillChannelRouteKeys()
	require.NoError(t, err)
	assert.Equal(t, 2, filled)

	var afterFirst []Channel
	require.NoError(t, DB.Order("id ASC").Find(&afterFirst).Error)
	require.Len(t, afterFirst, 3)
	for _, channel := range afterFirst {
		assert.Truef(t, IsValidChannelRouteKey(channel.GetRouteKey()),
			"channel %d route key %q is not valid after backfill", channel.Id, channel.GetRouteKey())
	}

	filledAgain, err := BackfillChannelRouteKeys()
	require.NoError(t, err)
	assert.Zero(t, filledAgain)

	var afterSecond []Channel
	require.NoError(t, DB.Order("id ASC").Find(&afterSecond).Error)
	assert.Equal(t, afterFirst, afterSecond)

	var preserved Channel
	require.NoError(t, DB.Where("name = ?", "backfill-existing").First(&preserved).Error)
	assert.Equal(t, existing, preserved.GetRouteKey())
}

func TestChannelUpdateDoesNotClobberRouteKey(t *testing.T) {
	resetChannelRouteKeys(t)

	channel := Channel{Name: "route-key-update", Key: "sk-update"}
	require.NoError(t, DB.Create(&channel).Error)
	original := channel.GetRouteKey()
	require.True(t, IsValidChannelRouteKey(original))

	// A request that omits route_key leaves the pointer nil; the update path must
	// not write NULL over the stored identity.
	stale := Channel{Id: channel.Id, Name: "route-key-updated"}
	require.NoError(t, stale.Update())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, "route-key-updated", stored.Name)
	assert.Equal(t, original, stored.GetRouteKey())
}

func TestRouteGroupNaming(t *testing.T) {
	const key = "ch_0123456789AbCdEf"
	assert.Equal(t, "__route_"+key, RouteGroupName(key))
	assert.True(t, IsRouteGroup(RouteGroupName(key)))
	assert.False(t, IsRouteGroup("default"))
	assert.False(t, IsRouteGroup(""))

	for _, tc := range []struct {
		group  string
		key    string
		valid  bool
		reason string
	}{
		{group: RouteGroupName(key), key: key, valid: true},
		{group: "default", reason: "an official group is not a route group"},
		{group: DirectRouteGroupPrefix, reason: "the bare prefix carries no identity"},
		{group: RouteGroupName("ch_tooShort"), reason: "a malformed identity never resolves"},
		{group: "__route_default", reason: "a group name that only looks prefixed is rejected"},
		{group: "__x" + key, reason: "a different prefix is not a route group"},
	} {
		got, ok := RouteKeyFromGroup(tc.group)
		assert.Equal(t, tc.valid, ok, "group %q: %s", tc.group, tc.reason)
		assert.Equal(t, tc.key, got, "group %q", tc.group)
	}

	// The virtual group name is derived, never stored: no ability row may carry it.
	assert.True(t, IsValidChannelRouteKey(key))
	assert.NotContains(t, RouteGroupName(key), " ")
}

// newRouteGroupTestChannels inserts one enabled and one disabled channel with
// their own route identities and points the test at a cache mode. It returns the
// enabled channel, the disabled one, and the enabled channel's route group name.
func newRouteGroupTestChannels(t *testing.T, memoryCache bool) (*Channel, *Channel, string) {
	t.Helper()
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = memoryCache
	for _, table := range []string{"abilities", "channels"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"abilities", "channels"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
		common.MemoryCacheEnabled = previousCache
		InitChannelCache()
	})

	enabled := &Channel{
		Name: "route-group-enabled", Key: "sk-enabled", Status: common.ChannelStatusEnabled,
		Group: "default", Models: "claude-3-7-sonnet,gpt-4o",
	}
	require.NoError(t, DB.Create(enabled).Error)
	disabled := &Channel{
		Name: "route-group-disabled", Key: "sk-disabled", Status: common.ChannelStatusManuallyDisabled,
		Group: "default", Models: "claude-3-7-sonnet",
	}
	require.NoError(t, DB.Create(disabled).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "claude-3-7-sonnet", ChannelId: enabled.Id, Enabled: true}).Error)
	InitChannelCache()
	require.True(t, IsValidChannelRouteKey(enabled.GetRouteKey()))
	return enabled, disabled, RouteGroupName(enabled.GetRouteKey())
}

// TestResolveRouteGroupChannel covers both lookup paths of the virtual group
// resolver: every rejection reason returns nil so the auto-group loop can move
// on to the next entry.
func TestResolveRouteGroupChannel(t *testing.T) {
	for _, memoryCache := range []bool{true, false} {
		t.Run(map[bool]string{true: "memory cache", false: "database"}[memoryCache], func(t *testing.T) {
			enabled, disabled, group := newRouteGroupTestChannels(t, memoryCache)

			resolved := resolveRouteGroupChannel(group, "claude-3-7-sonnet", nil)
			require.NotNil(t, resolved, "an enabled channel that serves the model resolves")
			assert.Equal(t, enabled.Id, resolved.Id)

			// RoutingMatchModelName strips the thinking suffix, matching the
			// second lookup of GetRandomSatisfiedChannel.
			resolved = resolveRouteGroupChannel(group, "claude-3-7-sonnet-thinking", nil)
			require.NotNil(t, resolved, "the normalized model name is tried as well")
			assert.Equal(t, enabled.Id, resolved.Id)

			assert.Nil(t, resolveRouteGroupChannel(group, "gpt-5.1", nil), "a model the channel does not serve resolves to nothing")
			assert.Nil(t, resolveRouteGroupChannel(RouteGroupName(disabled.GetRouteKey()), "claude-3-7-sonnet", nil),
				"a disabled channel resolves to nothing")
			assert.Nil(t, resolveRouteGroupChannel(RouteGroupName("ch_0123456789AbCdEf"), "claude-3-7-sonnet", nil),
				"an unknown identity resolves to nothing")
			assert.Nil(t, resolveRouteGroupChannel("default", "claude-3-7-sonnet", nil),
				"an official group is not resolved here")
			assert.NotNil(t, resolveRouteGroupChannel(group, "claude-3-7-sonnet", []dto.ChannelFilter{
				{Kind: dto.FilterRequestPath, RequestPath: "/v1/messages"},
			}), "a request path filter only constrains advanced custom channels")

			// GetRandomSatisfiedChannel is the only channel entry the relay uses.
			channel, err := GetRandomSatisfiedChannel(group, "claude-3-7-sonnet", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, enabled.Id, channel.Id)
			channel, err = GetRandomSatisfiedChannel(group, "gpt-5.1", 2, nil)
			require.NoError(t, err)
			assert.Nil(t, channel, "an unavailable route group returns no channel and no error")
		})
	}
}

// TestResolveRouteGroupChannelHonorsAdvancedCustomRoute proves the request-path
// filter is not silently dropped for the advanced custom channel type.
func TestResolveRouteGroupChannelHonorsAdvancedCustomRoute(t *testing.T) {
	resetChannelRouteKeys(t)
	channel := &Channel{
		Name: "route-group-advanced", Key: "sk-advanced", Type: constant.ChannelTypeAdvancedCustom,
		Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-4o",
	}
	channel.SetOtherSettings(kitdto.ChannelOtherSettings{AdvancedCustom: &kitdto.AdvancedCustomConfig{
		Routes: []kitdto.AdvancedCustomRoute{{IncomingPath: "/v1/messages", UpstreamPath: "/v1/messages"}},
	}})
	require.NoError(t, DB.Create(channel).Error)
	group := RouteGroupName(channel.GetRouteKey())

	require.NotNil(t, resolveRouteGroupChannel(group, "gpt-4o", []dto.ChannelFilter{
		{Kind: dto.FilterRequestPath, RequestPath: "/v1/messages"},
	}), "the configured route is not filtered out")
	assert.Nil(t, resolveRouteGroupChannel(group, "gpt-4o", []dto.ChannelFilter{
		{Kind: dto.FilterRequestPath, RequestPath: "/v1/responses"},
	}), "a path the channel does not serve is filtered out")
}

// TestRouteKeyIndexTracksCacheUpdates pins the cache index behind the virtual
// groups: a rebuilt cache resolves every stored identity, and a channel whose
// identity changes stops resolving under the old one.
func TestRouteKeyIndexTracksCacheUpdates(t *testing.T) {
	enabled, disabled, group := newRouteGroupTestChannels(t, true)

	assert.Nil(t, resolveRouteGroupChannel(RouteGroupName(disabled.GetRouteKey()), "claude-3-7-sonnet", nil),
		"a disabled channel stays indexed but never resolves")

	replacement := "ch_replacementKey01"
	updated := *enabled
	updated.RouteKey = &replacement
	CacheUpdateChannel(&updated)

	assert.NotNil(t, resolveRouteGroupChannel(RouteGroupName(replacement), "claude-3-7-sonnet", nil),
		"the new identity resolves after the cache update")
	assert.Nil(t, resolveRouteGroupChannel(group, "claude-3-7-sonnet", nil),
		"the previous identity no longer resolves")
}

func TestGetGroupEnabledModelsForRouteGroup(t *testing.T) {
	_, disabled, group := newRouteGroupTestChannels(t, true)

	assert.Equal(t, []string{"claude-3-7-sonnet", "gpt-4o"}, GetGroupEnabledModels(group))
	assert.Empty(t, GetGroupEnabledModels(RouteGroupName(disabled.GetRouteKey())),
		"a disabled channel exposes no model")
	assert.Empty(t, GetGroupEnabledModels(RouteGroupName("ch_0123456789AbCdEf")))
}

func TestIsChannelEnabledForGroupModelForRouteGroup(t *testing.T) {
	enabled, disabled, group := newRouteGroupTestChannels(t, true)

	assert.True(t, IsChannelEnabledForGroupModel(group, "claude-3-7-sonnet", enabled.Id))
	assert.True(t, IsChannelEnabledForGroupModel(group, "claude-3-7-sonnet-thinking", enabled.Id),
		"the normalized model name is accepted")
	assert.False(t, IsChannelEnabledForGroupModel(group, "claude-3-7-sonnet", disabled.Id))
	assert.False(t, IsChannelEnabledForGroupModel(group, "gpt-5.1", enabled.Id))
	assert.False(t, IsChannelEnabledForGroupModel(RouteGroupName(disabled.GetRouteKey()), "claude-3-7-sonnet", disabled.Id),
		"a disabled channel is not enabled for its own route group")
}

func TestListChannelRouteOptionsOmitsSecrets(t *testing.T) {
	resetChannelRouteKeys(t)

	tag := "route-option-tag"
	first := Channel{Name: "route-option-first", Key: "sk-first", Models: "gpt-4o"}
	require.NoError(t, DB.Create(&first).Error)
	second := Channel{Name: "route-option-second", Key: "sk-second", Type: 2, Models: "gpt-4o", Tag: &tag}
	require.NoError(t, DB.Create(&second).Error)

	options, err := ListChannelRouteOptions()
	require.NoError(t, err)
	require.Len(t, options, 2)
	assert.Equal(t, first.Id, options[0].Id)
	assert.Equal(t, second.Id, options[1].Id)
	assert.Equal(t, "route-option-first", options[0].Name)
	assert.Equal(t, 2, options[1].Type)
	assert.Equal(t, &tag, options[1].Tag)
	for _, option := range options {
		require.NotNil(t, option.RouteKey)
		assert.True(t, IsValidChannelRouteKey(*option.RouteKey))
	}

	payload, err := common.Marshal(options)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "sk-second")
	assert.NotContains(t, string(payload), `"key"`)
	assert.NotContains(t, string(payload), `"models"`)
}

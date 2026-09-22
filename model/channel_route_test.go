package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
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

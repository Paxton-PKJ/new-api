package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTokenModelMapping(t *testing.T) {
	tooLongName := strings.Repeat("x", MaxTokenModelMappingNameLength+1)
	oversized := `{"a":"` + strings.Repeat("x", MaxTokenModelMappingBytes) + `"}`

	cases := []struct {
		name       string
		raw        string
		want       map[string]string
		wantErr    string
		cycle      bool
		cycleNames []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace", raw: "  ", want: nil},
		{name: "empty object", raw: "{}", want: nil},
		{name: "null", raw: "null", want: nil},
		{
			name: "trims names",
			raw:  `{"claude-opus-4-8":"dsv4f"," b ":" c "}`,
			want: map[string]string{"claude-opus-4-8": "dsv4f", "b": "c"},
		},
		{name: "invalid json", raw: `{"a":`, wantErr: "model redirect must be a JSON object"},
		{name: "top level array", raw: `[1]`, wantErr: "model redirect must be a JSON object"},
		{name: "top level string", raw: `"x"`, wantErr: "model redirect must be a JSON object"},
		{name: "non string value", raw: `{"a":1}`, wantErr: `value for model "a" must be a string`},
		{name: "nested object value", raw: `{"a":{"b":"c"}}`, wantErr: `value for model "a" must be a string`},
		{name: "empty source", raw: `{"":"x"}`, wantErr: "model name must not be empty"},
		{name: "empty target", raw: `{"a":""}`, wantErr: "model name must not be empty"},
		{name: "newline in name", raw: "{\"a\\nb\":\"c\"}", wantErr: "contains control characters"},
		{name: "nul in name", raw: "{\"a\\u0000b\":\"c\"}", wantErr: "contains control characters"},
		{name: "overlong name", raw: fmt.Sprintf(`{"%s":"c"}`, tooLongName), wantErr: "exceeds 256 characters"},
		{name: "oversized payload", raw: oversized, wantErr: "exceeds 65536 bytes"},
		{name: "duplicate source", raw: `{"a":"x"," a":"y"}`, wantErr: `duplicate source model "a"`},
		{name: "two node cycle", raw: `{"a":"b","b":"a"}`, cycle: true, cycleNames: []string{"a", "b"}},
		{name: "three node cycle", raw: `{"a":"b","b":"c","c":"a"}`, cycle: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTokenModelMapping(tc.raw)
			if tc.wantErr == "" && !tc.cycle {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
				return
			}
			require.Error(t, err)
			assert.Nil(t, got)
			if tc.wantErr != "" {
				assert.Contains(t, err.Error(), tc.wantErr)
			}
			if tc.cycle {
				require.ErrorIs(t, err, ErrTokenModelMappingCycle)
				assert.Contains(t, err.Error(), "->")
				for _, name := range tc.cycleNames {
					assert.Contains(t, err.Error(), name)
				}
			}
		})
	}
}

func TestResolveTokenModelMapping(t *testing.T) {
	cases := []struct {
		name    string
		mapping map[string]string
		model   string
		want    string
		cycle   bool
	}{
		{name: "single hop", mapping: map[string]string{"A": "B"}, model: "A", want: "B"},
		{name: "chained hops", mapping: map[string]string{"A": "B", "B": "C"}, model: "A", want: "C"},
		{name: "origin self reference", mapping: map[string]string{"A": "A"}, model: "A", want: "A"},
		{name: "mid chain self reference", mapping: map[string]string{"A": "B", "B": "B"}, model: "A", want: "B"},
		{name: "two node cycle", mapping: map[string]string{"A": "B", "B": "A"}, model: "A", cycle: true},
		{name: "three node cycle", mapping: map[string]string{"A": "B", "B": "C", "C": "A"}, model: "A", cycle: true},
		{name: "miss returns original", mapping: map[string]string{"A": "B"}, model: "Z", want: "Z"},
		{name: "nil mapping", mapping: nil, model: "A", want: "A"},
		{name: "empty mapping", mapping: map[string]string{}, model: "A", want: "A"},
		{name: "empty target means unmapped", mapping: map[string]string{"A": ""}, model: "A", want: "A"},
		{name: "empty model name", mapping: map[string]string{"A": "B"}, model: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTokenModelMapping(tc.mapping, tc.model)
			if tc.cycle {
				require.ErrorIs(t, err, ErrTokenModelMappingCycle)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTokenModelMappingPersistence(t *testing.T) {
	truncateTables(t)
	token := Token{
		UserId:         7,
		Key:            "token-model-mapping-persist-key",
		Name:           "mapping-persist",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.SetModelMapping(map[string]string{"claude-opus-4-8": "dsv4f", "b": "c"}))
	require.NoError(t, token.Insert())

	loaded, err := GetTokenById(token.Id)
	require.NoError(t, err)
	require.NotNil(t, loaded.ModelMapping)
	// common.Marshal 保证键有序且紧凑。
	assert.Equal(t, `{"b":"c","claude-opus-4-8":"dsv4f"}`, *loaded.ModelMapping)

	token.Name = "mapping-persist-renamed"
	require.NoError(t, token.Update())
	reloaded, err := GetTokenById(token.Id)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ModelMapping)
	assert.Equal(t, `{"b":"c","claude-opus-4-8":"dsv4f"}`, *reloaded.ModelMapping)

	require.NoError(t, token.SetModelMapping(nil))
	require.NoError(t, token.Update())
	cleared, err := GetTokenById(token.Id)
	require.NoError(t, err)
	assert.Nil(t, cleared.ModelMapping, "clearing the mapping must store NULL, not an empty string")

	var nullRows int64
	require.NoError(t, DB.Raw("SELECT COUNT(*) FROM tokens WHERE id = ? AND model_mapping IS NULL", token.Id).Scan(&nullRows).Error)
	assert.EqualValues(t, 1, nullRows)
}

func TestTokenModelMappingRoundTripThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	allowIps := "127.0.0.1"
	mapping := `{"b":"c","claude-opus-4-8":"dsv4f"}`
	profiles := `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"}}]}`
	token := Token{
		Id:                 42,
		UserId:             7,
		Key:                "token-model-mapping-cache-key",
		Status:             common.TokenStatusEnabled,
		Name:               "mapping-cache",
		CreatedTime:        1700000000,
		AccessedTime:       1700000001,
		ExpiredTime:        1700009999,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-4o,claude-opus-4-8",
		ModelMapping:       &mapping,
		Profiles:           &profiles,
		AllowIps:           &allowIps,
		RemainQuota:        123456,
		UsedQuota:          654321,
		Group:              "vip",
		CrossGroupRetry:    true,
		AutoGroups:         `["default","vip"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)

	assert.Equal(t, token.Id, cached.Id)
	assert.Equal(t, token.UserId, cached.UserId)
	assert.Equal(t, token.Status, cached.Status)
	assert.Equal(t, token.Name, cached.Name)
	assert.Equal(t, token.CreatedTime, cached.CreatedTime)
	assert.Equal(t, token.AccessedTime, cached.AccessedTime)
	assert.Equal(t, token.ExpiredTime, cached.ExpiredTime)
	assert.Equal(t, token.UnlimitedQuota, cached.UnlimitedQuota)
	assert.Equal(t, token.ModelLimitsEnabled, cached.ModelLimitsEnabled)
	assert.Equal(t, token.ModelLimits, cached.ModelLimits)
	assert.Equal(t, token.ModelMapping, cached.ModelMapping)
	assert.Equal(t, token.Profiles, cached.Profiles)
	assert.Equal(t, profiles, cached.GetProfiles())
	assert.Equal(t, token.AllowIps, cached.AllowIps)
	assert.Equal(t, token.RemainQuota, cached.RemainQuota)
	assert.Equal(t, token.UsedQuota, cached.UsedQuota)
	assert.Equal(t, token.Group, cached.Group)
	assert.Equal(t, token.CrossGroupRetry, cached.CrossGroupRetry)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	// TTL 参数若错位（例如仍指向 ModelMapping 的值），这里会暴露出来。
	assert.Greater(t, server.TTL(getTokenCacheKey(token.Key)), time.Duration(0))

	unmapped := Token{
		Id:             43,
		UserId:         7,
		Key:            "token-model-mapping-cache-nil-key",
		Status:         common.TokenStatusEnabled,
		Name:           "mapping-cache-nil",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, cacheSetTokenForTest(unmapped))
	cachedUnmapped, err := cacheGetTokenByKey(unmapped.Key)
	require.NoError(t, err)
	assert.Nil(t, cachedUnmapped.ModelMapping)
	assert.Empty(t, cachedUnmapped.GetModelMapping())
	assert.Nil(t, cachedUnmapped.Profiles)
	assert.Empty(t, cachedUnmapped.GetProfiles())
}

func TestTokenUpdateInvalidatesPreheatedModelMappingCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:         7,
		Key:            "token-model-mapping-update-cache-key",
		Name:           "mapping-cache-update",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.SetModelMapping(map[string]string{"old-model": "legacy-target"}))
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.NotNil(t, preheated.ModelMapping)
	assert.JSONEq(t, `{"old-model":"legacy-target"}`, *preheated.ModelMapping)

	require.NoError(t, token.SetModelMapping(map[string]string{"new-model": "new-target"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ModelMapping)
	assert.JSONEq(t, `{"new-model":"new-target"}`, *reloaded.ModelMapping)
}

func TestGetTokenByKeyWithoutRedisKeepsModelMapping(t *testing.T) {
	truncateTables(t)
	require.False(t, common.RedisEnabled)

	token := Token{
		UserId:         7,
		Key:            "token-model-mapping-no-redis-key",
		Name:           "mapping-no-redis",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.SetModelMapping(map[string]string{"claude-opus-4-8": "dsv4f"}))
	require.NoError(t, token.Insert())

	loaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.NotNil(t, loaded.ModelMapping)
	assert.Equal(t, `{"claude-opus-4-8":"dsv4f"}`, *loaded.ModelMapping)

	validated, err := ValidateUserToken(token.Key)
	require.NoError(t, err)
	require.NotNil(t, validated.ModelMapping)
	assert.Equal(t, *loaded.ModelMapping, *validated.ModelMapping)
}

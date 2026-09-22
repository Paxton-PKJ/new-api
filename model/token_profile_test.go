package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTokenProfiles(t *testing.T) {
	tooLongName := strings.Repeat("x", MaxTokenProfileNameLength+1)
	tooLongLimit := strings.Repeat("y", MaxTokenModelMappingNameLength+1)
	oversized := `{"profiles":[{"name":"p","model_mapping":{"a":"` + strings.Repeat("x", MaxTokenProfilesBytes) + `"}}]}`

	var manyProfiles strings.Builder
	manyProfiles.WriteString(`{"profiles":[`)
	for i := range MaxTokenProfiles + 1 {
		if i > 0 {
			manyProfiles.WriteString(",")
		}
		fmt.Fprintf(&manyProfiles, `{"name":"p%d"}`, i)
	}
	manyProfiles.WriteString(`]}`)

	var manyPresets strings.Builder
	manyPresets.WriteString(`{"profiles":[{"name":"p","route_presets":[`)
	for i := range MaxTokenRoutePresets + 1 {
		if i > 0 {
			manyPresets.WriteString(",")
		}
		fmt.Fprintf(&manyPresets, `{"name":"n%d","auto_groups":["a"]}`, i)
	}
	manyPresets.WriteString(`]}]}`)

	cases := []struct {
		name    string
		raw     string
		want    *TokenProfileConfig
		wantErr string
		cycle   bool
	}{
		{name: "empty", raw: ""},
		{name: "whitespace", raw: "  "},
		{name: "empty object", raw: "{}"},
		{name: "null", raw: "null"},
		{name: "invalid json", raw: `{"profiles":`, wantErr: "profiles must be a JSON object"},
		{name: "top level array", raw: `[1]`, wantErr: "profiles must be a JSON object"},
		{name: "top level string", raw: `"x"`, wantErr: "profiles must be a JSON object"},
		{name: "profiles not an array", raw: `{"profiles":{}}`, wantErr: "profiles must be a JSON array"},
		{name: "active profile without profiles", raw: `{"active_profile":"p"}`, wantErr: `active profile "p" is not configured`},
		{name: "empty profiles with active profile", raw: `{"active_profile":"p","profiles":[]}`, wantErr: `active profile "p" is not configured`},
		{name: "active profile is not a string", raw: `{"active_profile":1,"profiles":[{"name":"p"}]}`, wantErr: "active_profile must be a string"},
		{name: "too many profiles", raw: manyProfiles.String(), wantErr: "at most 16 profiles"},
		{name: "profile is not an object", raw: `{"profiles":["p"]}`, wantErr: "profile 0 must be a JSON object"},
		{name: "empty profile name", raw: `{"profiles":[{"name":" "}]}`, wantErr: "profile name must not be empty"},
		{name: "missing profile name", raw: `{"profiles":[{}]}`, wantErr: "profile name must not be empty"},
		{name: "profile name is not a string", raw: `{"profiles":[{"name":1}]}`, wantErr: "profile name must be a string"},
		{name: "newline in profile name", raw: `{"profiles":[{"name":"a\nb"}]}`, wantErr: "contains control characters"},
		{name: "overlong profile name", raw: fmt.Sprintf(`{"profiles":[{"name":%q}]}`, tooLongName), wantErr: "exceeds 64 characters"},
		{name: "duplicate profile name", raw: `{"profiles":[{"name":"p"},{"name":" p "}]}`, wantErr: `duplicate profile name "p"`},
		{name: "active profile not found", raw: `{"active_profile":"q","profiles":[{"name":"p"}]}`, wantErr: `active profile "q" is not configured`},
		{name: "model limits not an array", raw: `{"profiles":[{"name":"p","model_limits":"dsv4f"}]}`, wantErr: `profile "p": model limits must be a JSON array`},
		{name: "model limit is not a string", raw: `{"profiles":[{"name":"p","model_limits":["dsv4f",1]}]}`, wantErr: `profile "p": model limit must be a string`},
		{name: "empty model limit", raw: `{"profiles":[{"name":"p","model_limits":[" "]}]}`, wantErr: `profile "p": model limit must not be empty`},
		{name: "overlong model limit", raw: fmt.Sprintf(`{"profiles":[{"name":"p","model_limits":[%q]}]}`, tooLongLimit), wantErr: "exceeds 256 characters"},
		{name: "duplicate model limit", raw: `{"profiles":[{"name":"p","model_limits":["dsv4f"," dsv4f "]}]}`, wantErr: `profile "p": duplicate model limit "dsv4f"`},
		{name: "route presets not an array", raw: `{"profiles":[{"name":"p","route_presets":"n"}]}`, wantErr: `profile "p": route presets must be a JSON array`},
		{name: "route preset is not an object", raw: `{"profiles":[{"name":"p","route_presets":[1]}]}`, wantErr: `profile "p": route preset 0 must be a JSON object`},
		{name: "too many route presets", raw: manyPresets.String(), wantErr: "at most 8 presets"},
		{name: "empty route preset name", raw: `{"profiles":[{"name":"p","route_presets":[{"name":" ","auto_groups":["a"]}]}]}`, wantErr: "route preset name must not be empty"},
		{name: "duplicate route preset name", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":["a"]},{"name":" n ","auto_groups":["b"]}]}]}`, wantErr: `profile "p": duplicate route preset name "n"`},
		{name: "route preset without auto groups", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n"}]}]}`, wantErr: `route preset "n" must configure either auto_groups or route_keys`},
		{name: "route preset with empty auto groups", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":[]}]}]}`, wantErr: `route preset "n" must configure either auto_groups or route_keys`},
		{name: "route preset with empty route keys", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":[]}]}]}`, wantErr: `route preset "n" must configure either auto_groups or route_keys`},
		{name: "route preset with groups and keys", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":["a"],"route_keys":["ch_0123456789AbCdEf"]}]}]}`, wantErr: `route preset "n" must not configure both auto_groups and route_keys`},
		{name: "route keys not an array", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":"ch_0123456789AbCdEf"}]}]}`, wantErr: `profile "p": route preset "n": route keys must be a JSON array`},
		{name: "route key is not a string", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":[1]}]}]}`, wantErr: `profile "p": route preset "n": route key must be a string`},
		{name: "empty route key", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":[" "]}]}]}`, wantErr: `route preset "n": route key "" is invalid`},
		{name: "malformed route key", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":["ch_short"]}]}]}`, wantErr: `route preset "n": route key "ch_short" is invalid`},
		{name: "duplicate route key", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","route_keys":["ch_0123456789AbCdEf"," ch_0123456789AbCdEf "]}]}]}`, wantErr: `route preset "n": duplicate route key "ch_0123456789AbCdEf"`},
		{name: "auto group is not a string", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":[1]}]}]}`, wantErr: `profile "p": route preset "n": auto group must be a string`},
		{name: "empty auto group", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":[" "]}]}]}`, wantErr: "auto group must not be empty"},
		{name: "duplicate auto group", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":["a"," a "]}]}]}`, wantErr: `duplicate auto group "a"`},
		{name: "cross group retry is not a boolean", raw: `{"profiles":[{"name":"p","route_presets":[{"name":"n","auto_groups":["a"],"cross_group_retry":"yes"}]}]}`, wantErr: "cross_group_retry must be a boolean"},
		{name: "active route preset not found", raw: `{"profiles":[{"name":"p","active_route_preset":"missing","route_presets":[{"name":"n","auto_groups":["a"]}]}]}`, wantErr: `profile "p": active route preset "missing" is not configured`},
		{name: "active route preset is not a string", raw: `{"profiles":[{"name":"p","active_route_preset":1}]}`, wantErr: `profile "p": active route preset must be a string`},
		{
			name: "direct route preset",
			raw:  `{"active_profile":" p ","profiles":[{"name":"p","active_route_preset":" direct ","route_presets":[{"name":" direct ","route_keys":[" ch_0123456789AbCdEf ","ch_FEDCBA9876543210"],"cross_group_retry":true}]}]}`,
			want: &TokenProfileConfig{
				ActiveProfile: "p",
				Profiles: []TokenProfile{{
					Name:              "p",
					ActiveRoutePreset: "direct",
					RoutePresets: []TokenRoutePreset{
						{Name: "direct", RouteKeys: []string{"ch_0123456789AbCdEf", "ch_FEDCBA9876543210"}, CrossGroupRetry: true},
					},
				}},
			},
		},
		{name: "cycle in profile model mapping", raw: `{"profiles":[{"name":"p","model_mapping":{"a":"b","b":"a"}}]}`, cycle: true},
		{name: "non string profile model mapping value", raw: `{"profiles":[{"name":"p","model_mapping":{"a":1}}]}`, wantErr: `profile "p": value for model "a" must be a string`},
		{name: "oversized payload", raw: oversized, wantErr: "exceeds 65536 bytes"},
		{
			name: "normalizes a full document",
			raw: `{"active_profile":" dsv4f ","profiles":[` +
				`{"name":" dsv4f ","model_mapping":{"claude-opus-4-8":"dsv4f"},"model_limits":["dsv4f"," gpt-4o "],` +
				`"active_route_preset":" normal ","route_presets":[` +
				`{"name":"normal","auto_groups":["fengwind"," agent "],"cross_group_retry":true},` +
				`{"name":"agent-first","auto_groups":["agent","fengwind"],"cross_group_retry":true}]}]}`,
			want: &TokenProfileConfig{
				ActiveProfile: "dsv4f",
				Profiles: []TokenProfile{{
					Name:              "dsv4f",
					ModelMapping:      map[string]string{"claude-opus-4-8": "dsv4f"},
					ModelLimits:       []string{"dsv4f", "gpt-4o"},
					ActiveRoutePreset: "normal",
					RoutePresets: []TokenRoutePreset{
						{Name: "normal", AutoGroups: []string{"fengwind", "agent"}, CrossGroupRetry: true},
						{Name: "agent-first", AutoGroups: []string{"agent", "fengwind"}, CrossGroupRetry: true},
					},
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTokenProfiles(tc.raw)
			if tc.wantErr == "" && !tc.cycle {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
				if got != nil {
					// 规范化输出必须能再次通过严格解析。
					canonical, err := common.Marshal(got)
					require.NoError(t, err)
					reparsed, err := ParseTokenProfiles(string(canonical))
					require.NoError(t, err)
					assert.Equal(t, got, reparsed)
				}
				return
			}
			require.Error(t, err)
			assert.Nil(t, got)
			if tc.wantErr != "" {
				assert.Contains(t, err.Error(), tc.wantErr)
			}
			if tc.cycle {
				require.ErrorIs(t, err, ErrTokenModelMappingCycle)
				assert.Contains(t, err.Error(), `profile "p"`)
			}
		})
	}
}

// TestTokenProfileAutoGroupsSerializationIsStable pins the bytes stored for a
// legacy document that only uses auto_groups: adding route_keys to the preset
// must not rewrite what existing tokens already hold.
func TestTokenProfileAutoGroupsSerializationIsStable(t *testing.T) {
	const document = `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"},"active_route_preset":"normal","route_presets":[{"name":"normal","auto_groups":["fengwind","agent"],"cross_group_retry":true}]}]}`

	config, err := ParseTokenProfiles(document)
	require.NoError(t, err)
	var token Token
	require.NoError(t, token.SetProfileConfig(config))
	require.NotNil(t, token.Profiles)
	assert.Equal(t, document, *token.Profiles)
}

func TestResolveActiveProfile(t *testing.T) {
	const (
		mappingOnlyProfiles = `{"active_profile":"p","profiles":[{"name":"p","model_mapping":{"claude-opus-4-8":"dsv4f"}}]}`
		limitsOnlyProfiles  = `{"active_profile":"p","profiles":[{"name":"p","model_limits":["dsv4f"]}]}`
		fullProfiles        = `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"},"model_limits":["dsv4f"],"active_route_preset":"normal","route_presets":[{"name":"normal","auto_groups":["fengwind","agent"],"cross_group_retry":true},{"name":"agent-first","auto_groups":["agent"],"cross_group_retry":false}]}]}`
		directProfiles      = `{"active_profile":"p","profiles":[{"name":"p","active_route_preset":"direct","route_presets":[{"name":"direct","route_keys":["ch_0123456789AbCdEf","ch_FEDCBA9876543210"],"cross_group_retry":true}]}]}`
		missingPreset       = `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"},"active_route_preset":"missing"}]}`
		inactiveProfiles    = `{"profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"}}]}`
		unknownProfiles     = `{"active_profile":"q","profiles":[{"name":"p","model_mapping":{"claude-opus-4-8":"dsv4f"}}]}`
		corruptProfiles     = `{"active_profile":"p","profiles":[{"name":"p","model_mapping":{"a":1}}]}`
	)

	baseToken := func() Token {
		return Token{
			Id:                 11,
			UserId:             7,
			Key:                "token-profile-resolve-key",
			Name:               "token-profile-resolve",
			Status:             common.TokenStatusEnabled,
			ExpiredTime:        -1,
			UnlimitedQuota:     true,
			Group:              "auto",
			ModelLimitsEnabled: true,
			ModelLimits:        "base-model",
			ModelMapping:       common.GetPointer(`{"a":"b"}`),
			AutoGroups:         `["base1","base2"]`,
			CrossGroupRetry:    false,
		}
	}
	withProfiles := func(token Token, raw string) Token {
		token.Profiles = common.GetPointer(raw)
		return token
	}

	// mappingPlusLimits 是完整配置档除 auto groups 外的叠加结果。
	mappingPlusLimits := func(token Token) Token {
		token.ModelMapping = common.GetPointer(`{"claude-opus-4-8":"dsv4f"}`)
		token.ModelLimitsEnabled = true
		token.ModelLimits = "dsv4f"
		return token
	}

	limitsDisabled := func() Token {
		token := baseToken()
		token.ModelLimitsEnabled = false
		token.ModelLimits = ""
		return token
	}
	defaultGroup := func() Token {
		token := baseToken()
		token.Group = "default"
		return token
	}

	tests := []struct {
		name          string
		token         Token
		sameToken     bool
		wantProfile   string
		wantPreset    string
		wantRouteKeys []string
		want          *Token
	}{
		{name: "no profiles", token: baseToken(), sameToken: true},
		{name: "blank profiles", token: withProfiles(baseToken(), " "), sameToken: true},
		{name: "empty object", token: withProfiles(baseToken(), "{}"), sameToken: true},
		{name: "invalid json", token: withProfiles(baseToken(), `{"active_profile":`), sameToken: true},
		{name: "inactive document", token: withProfiles(baseToken(), inactiveProfiles), sameToken: true},
		{name: "unknown active profile", token: withProfiles(baseToken(), unknownProfiles), sameToken: true},
		{name: "corrupt profile document", token: withProfiles(baseToken(), corruptProfiles), sameToken: true},
		{
			name:        "mapping only",
			token:       withProfiles(baseToken(), mappingOnlyProfiles),
			wantProfile: "p",
			want: func() *Token {
				token := withProfiles(baseToken(), mappingOnlyProfiles)
				token.ModelMapping = common.GetPointer(`{"claude-opus-4-8":"dsv4f"}`)
				return &token
			}(),
		},
		{
			name:        "limits enable and replace the base limits",
			token:       withProfiles(limitsDisabled(), limitsOnlyProfiles),
			wantProfile: "p",
			want: func() *Token {
				token := withProfiles(limitsDisabled(), limitsOnlyProfiles)
				token.ModelLimitsEnabled = true
				token.ModelLimits = "dsv4f"
				return &token
			}(),
		},
		{
			name:        "preset replaces the auto groups",
			token:       withProfiles(baseToken(), fullProfiles),
			wantProfile: "dsv4f",
			wantPreset:  "normal",
			want: func() *Token {
				token := withProfiles(mappingPlusLimits(baseToken()), fullProfiles)
				token.AutoGroups = `["fengwind","agent"]`
				token.CrossGroupRetry = true
				return &token
			}(),
		},
		{
			name:        "non auto group ignores the preset",
			token:       withProfiles(defaultGroup(), fullProfiles),
			wantProfile: "dsv4f",
			want: func() *Token {
				token := withProfiles(mappingPlusLimits(defaultGroup()), fullProfiles)
				return &token
			}(),
		},
		{
			name:        "unknown preset keeps the base groups",
			token:       withProfiles(baseToken(), missingPreset),
			wantProfile: "dsv4f",
			want: func() *Token {
				token := withProfiles(baseToken(), missingPreset)
				token.ModelMapping = common.GetPointer(`{"claude-opus-4-8":"dsv4f"}`)
				return &token
			}(),
		},
		{
			name:          "direct preset keeps the base groups and returns the route keys",
			token:         withProfiles(baseToken(), directProfiles),
			wantProfile:   "p",
			wantPreset:    "direct",
			wantRouteKeys: []string{"ch_0123456789AbCdEf", "ch_FEDCBA9876543210"},
			want: func() *Token {
				token := withProfiles(baseToken(), directProfiles)
				token.CrossGroupRetry = true
				return &token
			}(),
		},
		{
			name:        "non auto group ignores the direct preset",
			token:       withProfiles(defaultGroup(), directProfiles),
			wantProfile: "p",
			want: func() *Token {
				token := withProfiles(defaultGroup(), directProfiles)
				return &token
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.token
			resolved := base.ResolveActiveProfile()

			assert.Equal(t, tc.wantProfile, resolved.ProfileName)
			assert.Equal(t, tc.wantPreset, resolved.PresetName)
			assert.Equal(t, tc.wantRouteKeys, resolved.RouteKeys)
			if tc.sameToken {
				assert.True(t, resolved.Token == &base, "a token without an active profile is returned unchanged")
			} else {
				require.NotNil(t, tc.want)
				assert.True(t, resolved.Token != &base, "an overlaid token must be a copy")
				assert.Equal(t, *tc.want, *resolved.Token)
			}
			assert.Equal(t, tc.token, base, "the original token must never be modified")
		})
	}
}

// TestCountTokensWithActiveDirectRoutePreset counts only tokens whose active
// preset really selects channels by route key: a document that merely stores
// route_keys somewhere does not count.
func TestCountTokensWithActiveDirectRoutePreset(t *testing.T) {
	truncateTables(t)

	const directPreset = `{"name":"direct","route_keys":["ch_0123456789AbCdEf"]}`
	tokens := map[string]string{
		"stored but inactive":  `{"profiles":[{"name":"p","route_presets":[` + directPreset + `]}]}`,
		"active profile only":  `{"active_profile":"p","profiles":[{"name":"p","route_presets":[` + directPreset + `]}]}`,
		"active legacy preset": `{"active_profile":"p","profiles":[{"name":"p","active_route_preset":"legacy","route_presets":[{"name":"legacy","auto_groups":["default"]}]}]}`,
		"unknown profile":      `{"active_profile":"q","profiles":[{"name":"p","active_route_preset":"direct","route_presets":[` + directPreset + `]}]}`,
		"active direct preset": `{"active_profile":"p","profiles":[{"name":"p","active_route_preset":"direct","route_presets":[` + directPreset + `]}]}`,
	}
	for name, document := range tokens {
		token := Token{
			UserId:         7,
			Key:            "token-direct-count-" + name,
			Name:           name,
			Status:         common.TokenStatusEnabled,
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Profiles:       common.GetPointer(document),
		}
		require.NoError(t, token.Insert())
	}

	count, err := CountTokensWithActiveDirectRoutePreset()
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTokenProfilesPersistence(t *testing.T) {
	truncateTables(t)
	document := `{"active_profile":"dsv4f","profiles":[{"name":"dsv4f","model_mapping":{"claude-opus-4-8":"dsv4f"},"model_limits":["dsv4f"],"active_route_preset":"normal","route_presets":[{"name":"normal","auto_groups":["fengwind","agent"],"cross_group_retry":true}]}]}`
	config, err := ParseTokenProfiles(document)
	require.NoError(t, err)

	token := Token{
		UserId:         7,
		Key:            "token-profiles-persist-key",
		Name:           "profiles-persist",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, token.SetProfileConfig(config))
	require.NoError(t, token.Insert())

	loaded, err := GetTokenById(token.Id)
	require.NoError(t, err)
	require.NotNil(t, loaded.Profiles)
	// common.Marshal 保证字段有序且紧凑。
	assert.Equal(t, document, *loaded.Profiles)

	token.Name = "profiles-persist-renamed"
	require.NoError(t, token.Update())
	reloaded, err := GetTokenById(token.Id)
	require.NoError(t, err)
	require.NotNil(t, reloaded.Profiles)
	assert.Equal(t, document, *reloaded.Profiles)

	require.NoError(t, token.SetProfileConfig(nil))
	require.NoError(t, token.Update())
	cleared, err := GetTokenById(token.Id)
	require.NoError(t, err)
	assert.Nil(t, cleared.Profiles, "clearing the profiles must store NULL, not an empty string")

	var nullRows int64
	require.NoError(t, DB.Raw("SELECT COUNT(*) FROM tokens WHERE id = ? AND profiles IS NULL", token.Id).Scan(&nullRows).Error)
	assert.EqualValues(t, 1, nullRows)
}

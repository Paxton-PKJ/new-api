package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

const (
	// MaxTokenProfiles 限制单个令牌可保存的路由配置档数量。
	MaxTokenProfiles = 16
	// MaxTokenRoutePresets 限制单个配置档可保存的路由预设数量。
	MaxTokenRoutePresets = 8
	// MaxTokenProfileNameLength 限制配置档、路由预设与自动分组名的字节长度。
	MaxTokenProfileNameLength = 64
	// MaxTokenProfilesBytes 限制存储的 profiles JSON 总长度，保证每请求解析开销可控。
	MaxTokenProfilesBytes = 64 << 10
)

// TokenRoutePreset 是配置档内的一组路由参数：要么按官方分组（auto_groups），
// 要么按渠道路由身份（route_keys）直接选择渠道，两者恰好配置一个。
type TokenRoutePreset struct {
	Name            string   `json:"name"`
	AutoGroups      []string `json:"auto_groups,omitempty"`
	RouteKeys       []string `json:"route_keys,omitempty"`
	CrossGroupRetry bool     `json:"cross_group_retry"`
}

// IsDirect 表示预设按渠道路由身份直接选择渠道，而不是按官方分组。
func (preset TokenRoutePreset) IsDirect() bool { return len(preset.RouteKeys) > 0 }

// TokenProfile 是一套可整体切换到令牌上的路由配置。
type TokenProfile struct {
	Name              string             `json:"name"`
	ModelMapping      map[string]string  `json:"model_mapping,omitempty"`
	ModelLimits       []string           `json:"model_limits,omitempty"`
	ActiveRoutePreset string             `json:"active_route_preset,omitempty"`
	RoutePresets      []TokenRoutePreset `json:"route_presets,omitempty"`
}

// TokenProfileConfig 是令牌 profiles 列存储的 JSON 文档。
type TokenProfileConfig struct {
	ActiveProfile string         `json:"active_profile,omitempty"`
	Profiles      []TokenProfile `json:"profiles"`
}

// GetProfiles 返回存储的原始 JSON 字符串，未配置时为 ""。
func (token *Token) GetProfiles() string {
	if token.Profiles == nil {
		return ""
	}
	return *token.Profiles
}

// GetProfileConfig 宽松解析请求路径使用的 profiles 文档：未配置、无法解析或
// 解析后没有配置档时返回 nil。不做长度、名字与映射环校验，保存期由
// ParseTokenProfiles 严格把关。
func (token *Token) GetProfileConfig() *TokenProfileConfig {
	raw := strings.TrimSpace(token.GetProfiles())
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var config TokenProfileConfig
	if err := common.UnmarshalJsonStr(raw, &config); err != nil {
		return nil
	}
	if len(config.Profiles) == 0 {
		return nil
	}
	return &config
}

// SetProfileConfig 以规范化 JSON 存储 profile 文档；nil 或空 profiles 将字段清空为 NULL。
func (token *Token) SetProfileConfig(config *TokenProfileConfig) error {
	if config == nil || len(config.Profiles) == 0 {
		token.Profiles = nil
		return nil
	}
	data, err := common.Marshal(config)
	if err != nil {
		return err
	}
	token.Profiles = common.GetPointer(string(data))
	return nil
}

// ResolvedTokenProfile 是活动配置档叠加的结果：Token 是叠加后的副本（未叠加时是
// 原指针），RouteKeys 只在活动预设按渠道路由身份直接选择渠道时非空。
type ResolvedTokenProfile struct {
	Token       *Token
	ProfileName string
	PresetName  string
	RouteKeys   []string
}

// ResolveActiveProfile 把当前活动的配置档（及其活动路由预设）叠加到令牌副本上，
// 供请求路径复用令牌既有的字段消费方。未配置、active_profile 为空或找不到同名
// 配置档时返回原指针与空名字。
//
// 返回的副本从不写回数据库；未覆盖的字段（Key/Quota/Group/AllowIps 等）保持原值。
// Group 不是 auto、找不到预设或预设没有分组时静默跳过路由预设叠加。direct 预设
// （route_keys）只叠加跨分组重试并返回 RouteKeys 副本，渠道选择由调用方决定。
func (token *Token) ResolveActiveProfile() ResolvedTokenProfile {
	config := token.GetProfileConfig()
	if config == nil || config.ActiveProfile == "" {
		return ResolvedTokenProfile{Token: token}
	}
	index := slices.IndexFunc(config.Profiles, func(profile TokenProfile) bool {
		return profile.Name == config.ActiveProfile
	})
	if index < 0 {
		return ResolvedTokenProfile{Token: token}
	}

	profile := config.Profiles[index]
	copied := *token
	resolved := ResolvedTokenProfile{Token: &copied, ProfileName: profile.Name}
	if len(profile.ModelMapping) > 0 {
		if err := copied.SetModelMapping(profile.ModelMapping); err != nil {
			common.SysLog("failed to overlay token profile model mapping: " + err.Error())
		}
	}
	if len(profile.ModelLimits) > 0 {
		copied.ModelLimitsEnabled = true
		copied.ModelLimits = strings.Join(profile.ModelLimits, ",")
	}
	if profile.ActiveRoutePreset != "" && copied.Group == "auto" {
		presetIndex := slices.IndexFunc(profile.RoutePresets, func(preset TokenRoutePreset) bool {
			return preset.Name == profile.ActiveRoutePreset
		})
		if presetIndex >= 0 {
			preset := profile.RoutePresets[presetIndex]
			if preset.IsDirect() {
				// direct 预设不写 AutoGroups：那不是官方分组，也不该出现在
				// ContextKeyTokenAutoGroups 里，渠道选择留给调用方。
				copied.CrossGroupRetry = preset.CrossGroupRetry
				resolved.PresetName = preset.Name
				resolved.RouteKeys = slices.Clone(preset.RouteKeys)
			} else if len(preset.AutoGroups) > 0 {
				// 空分组列表无法表达“清空”意图，按无效预设跳过，保留令牌原有分组。
				if err := copied.SetAutoGroups(preset.AutoGroups); err != nil {
					common.SysLog("failed to overlay token route preset groups: " + err.Error())
				} else {
					copied.CrossGroupRetry = preset.CrossGroupRetry
					resolved.PresetName = preset.Name
				}
			}
		}
	}
	return resolved
}

// CountTokensWithActiveDirectRoutePreset 统计活动预设按渠道路由身份直接选择渠道的
// 令牌数量，供关闭功能开关前确认没有令牌仍在依赖它。
func CountTokensWithActiveDirectRoutePreset() (int, error) {
	var tokens []Token
	// LIKE 只是预筛：profiles 是 text 列，三库都支持，真正的判定在 Go 里做。
	if err := DB.Model(&Token{}).
		Where("profiles LIKE ?", `%"route_keys"%`).
		Select("id, profiles").
		Find(&tokens).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range tokens {
		config := tokens[i].GetProfileConfig()
		if config == nil || config.ActiveProfile == "" {
			continue
		}
		profileIndex := slices.IndexFunc(config.Profiles, func(profile TokenProfile) bool {
			return profile.Name == config.ActiveProfile && profile.ActiveRoutePreset != ""
		})
		if profileIndex < 0 {
			continue
		}
		profile := config.Profiles[profileIndex]
		presetIndex := slices.IndexFunc(profile.RoutePresets, func(preset TokenRoutePreset) bool {
			return preset.Name == profile.ActiveRoutePreset
		})
		if presetIndex >= 0 && profile.RoutePresets[presetIndex].IsDirect() {
			count++
		}
	}
	return count, nil
}

// ParseTokenProfiles 严格解析并校验待保存的 profiles 文档，供保存接口在落库前调用。
// 为空/空白/`{}`/`null` 视为未配置并返回 nil；结构非法、名字非法、数量超限、
// active_profile 或 active_route_preset 引用不存在、嵌套 model_mapping 非法时返回错误。
// 未知字段被忽略；返回的结构体是 trim 后的规范形态。
func ParseTokenProfiles(raw string) (*TokenProfileConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}
	if len(raw) > MaxTokenProfilesBytes {
		return nil, fmt.Errorf("profiles exceeds %d bytes", MaxTokenProfilesBytes)
	}
	var document map[string]any
	if err := common.UnmarshalJsonStr(raw, &document); err != nil {
		return nil, errors.New("profiles must be a JSON object")
	}
	activeProfile, err := tokenProfileStringField(document, "active_profile", "active_profile")
	if err != nil {
		return nil, err
	}
	activeProfile = strings.TrimSpace(activeProfile)

	var rawProfiles []any
	if value := document["profiles"]; value != nil {
		profiles, ok := value.([]any)
		if !ok {
			return nil, errors.New("profiles must be a JSON array")
		}
		rawProfiles = profiles
	}
	if len(rawProfiles) == 0 {
		if activeProfile != "" {
			return nil, fmt.Errorf("active profile %q is not configured", activeProfile)
		}
		return nil, nil
	}
	if len(rawProfiles) > MaxTokenProfiles {
		return nil, fmt.Errorf("profiles must contain at most %d profiles", MaxTokenProfiles)
	}

	config := &TokenProfileConfig{ActiveProfile: activeProfile, Profiles: make([]TokenProfile, 0, len(rawProfiles))}
	seenProfiles := make(map[string]bool, len(rawProfiles))
	for index, item := range rawProfiles {
		document, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("profile %d must be a JSON object", index)
		}
		profile, err := parseTokenProfile(document)
		if err != nil {
			return nil, err
		}
		if seenProfiles[profile.Name] {
			return nil, fmt.Errorf("duplicate profile name %q", profile.Name)
		}
		seenProfiles[profile.Name] = true
		config.Profiles = append(config.Profiles, profile)
	}
	if activeProfile != "" && !seenProfiles[activeProfile] {
		return nil, fmt.Errorf("active profile %q is not configured", activeProfile)
	}
	return config, nil
}

// parseTokenProfile 校验单个配置档；名字以外的错误以 `profile %q: ` 为前缀。
func parseTokenProfile(document map[string]any) (TokenProfile, error) {
	rawName, err := tokenProfileStringField(document, "name", "profile name")
	if err != nil {
		return TokenProfile{}, err
	}
	name, err := normalizeTokenProfileName("profile name", rawName)
	if err != nil {
		return TokenProfile{}, err
	}
	profile := TokenProfile{Name: name}

	if value, ok := document["model_mapping"]; ok && value != nil {
		// 复用令牌级映射的严格校验（名字、重复源、环），并采用其规范化结果。
		data, err := common.Marshal(value)
		if err != nil {
			return TokenProfile{}, fmt.Errorf("profile %q: model mapping must be a JSON object", name)
		}
		mapping, err := ParseTokenModelMapping(string(data))
		if err != nil {
			return TokenProfile{}, fmt.Errorf("profile %q: %w", name, err)
		}
		profile.ModelMapping = mapping
	}

	if value, ok := document["model_limits"]; ok && value != nil {
		items, ok := value.([]any)
		if !ok {
			return TokenProfile{}, fmt.Errorf("profile %q: model limits must be a JSON array", name)
		}
		limits := make([]string, 0, len(items))
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			limit, ok := item.(string)
			if !ok {
				return TokenProfile{}, fmt.Errorf("profile %q: model limit must be a string", name)
			}
			limit = strings.TrimSpace(limit)
			if limit == "" {
				return TokenProfile{}, fmt.Errorf("profile %q: model limit must not be empty", name)
			}
			if strings.ContainsFunc(limit, unicode.IsControl) {
				return TokenProfile{}, fmt.Errorf("profile %q: model limit %q contains control characters", name, limit)
			}
			if len(limit) > MaxTokenModelMappingNameLength {
				return TokenProfile{}, fmt.Errorf("profile %q: model limit %q exceeds %d characters", name, limit, MaxTokenModelMappingNameLength)
			}
			if seen[limit] {
				return TokenProfile{}, fmt.Errorf("profile %q: duplicate model limit %q", name, limit)
			}
			seen[limit] = true
			limits = append(limits, limit)
		}
		if len(limits) > 0 {
			profile.ModelLimits = limits
		}
	}

	var presets []TokenRoutePreset
	seenPresets := make(map[string]bool)
	if value, ok := document["route_presets"]; ok && value != nil {
		items, ok := value.([]any)
		if !ok {
			return TokenProfile{}, fmt.Errorf("profile %q: route presets must be a JSON array", name)
		}
		if len(items) > MaxTokenRoutePresets {
			return TokenProfile{}, fmt.Errorf("profile %q: route presets must contain at most %d presets", name, MaxTokenRoutePresets)
		}
		for index, item := range items {
			document, ok := item.(map[string]any)
			if !ok {
				return TokenProfile{}, fmt.Errorf("profile %q: route preset %d must be a JSON object", name, index)
			}
			preset, err := parseTokenRoutePreset(document)
			if err != nil {
				return TokenProfile{}, fmt.Errorf("profile %q: %w", name, err)
			}
			if seenPresets[preset.Name] {
				return TokenProfile{}, fmt.Errorf("profile %q: duplicate route preset name %q", name, preset.Name)
			}
			seenPresets[preset.Name] = true
			presets = append(presets, preset)
		}
	}
	if len(presets) > 0 {
		profile.RoutePresets = presets
	}

	rawPreset, err := tokenProfileStringField(document, "active_route_preset", "active route preset")
	if err != nil {
		return TokenProfile{}, fmt.Errorf("profile %q: %w", name, err)
	}
	if activePreset := strings.TrimSpace(rawPreset); activePreset != "" {
		if !seenPresets[activePreset] {
			return TokenProfile{}, fmt.Errorf("profile %q: active route preset %q is not configured", name, activePreset)
		}
		profile.ActiveRoutePreset = activePreset
	}
	return profile, nil
}

// parseTokenRoutePreset 校验单个路由预设；调用方负责补 `profile %q: ` 前缀。
// auto_groups 与 route_keys 必须恰好配置一个非空列表。
func parseTokenRoutePreset(document map[string]any) (TokenRoutePreset, error) {
	rawName, err := tokenProfileStringField(document, "name", "route preset name")
	if err != nil {
		return TokenRoutePreset{}, err
	}
	name, err := normalizeTokenProfileName("route preset name", rawName)
	if err != nil {
		return TokenRoutePreset{}, err
	}
	preset := TokenRoutePreset{Name: name}

	var groups []string
	if value, ok := document["auto_groups"]; ok && value != nil {
		items, ok := value.([]any)
		if !ok {
			return TokenRoutePreset{}, fmt.Errorf("route preset %q: auto groups must be a JSON array", name)
		}
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			group, ok := item.(string)
			if !ok {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: auto group must be a string", name)
			}
			group, err := normalizeTokenProfileName("auto group", group)
			if err != nil {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: %w", name, err)
			}
			if seen[group] {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: duplicate auto group %q", name, group)
			}
			seen[group] = true
			groups = append(groups, group)
		}
	}

	var keys []string
	if value, ok := document["route_keys"]; ok && value != nil {
		items, ok := value.([]any)
		if !ok {
			return TokenRoutePreset{}, fmt.Errorf("route preset %q: route keys must be a JSON array", name)
		}
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			key, ok := item.(string)
			if !ok {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: route key must be a string", name)
			}
			key = strings.TrimSpace(key)
			if !IsValidChannelRouteKey(key) {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: route key %q is invalid", name, key)
			}
			if seen[key] {
				return TokenRoutePreset{}, fmt.Errorf("route preset %q: duplicate route key %q", name, key)
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}

	switch {
	case len(groups) == 0 && len(keys) == 0:
		return TokenRoutePreset{}, fmt.Errorf("route preset %q must configure either auto_groups or route_keys", name)
	case len(groups) > 0 && len(keys) > 0:
		return TokenRoutePreset{}, fmt.Errorf("route preset %q must not configure both auto_groups and route_keys", name)
	}
	if len(groups) > 0 {
		preset.AutoGroups = groups
	}
	if len(keys) > 0 {
		preset.RouteKeys = keys
	}

	if value, ok := document["cross_group_retry"]; ok && value != nil {
		retry, ok := value.(bool)
		if !ok {
			return TokenRoutePreset{}, fmt.Errorf("route preset %q: cross_group_retry must be a boolean", name)
		}
		preset.CrossGroupRetry = retry
	}
	return preset, nil
}

// tokenProfileStringField 读取解码后 JSON 对象里的可选字符串字段，缺失或 null 视为空串。
func tokenProfileStringField(document map[string]any, key, label string) (string, error) {
	value, ok := document[key]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", label)
	}
	return text, nil
}

// normalizeTokenProfileName 校验并返回 trim 后的配置档、路由预设或自动分组名。
func normalizeTokenProfileName(kind, raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%s must not be empty", kind)
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("%s %q contains control characters", kind, name)
	}
	if len(name) > MaxTokenProfileNameLength {
		return "", fmt.Errorf("%s %q exceeds %d characters", kind, name, MaxTokenProfileNameLength)
	}
	return name, nil
}

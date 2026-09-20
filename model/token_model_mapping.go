package model

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

const (
	// MaxTokenModelMappingBytes 限制存储的 JSON 总长度，保证每请求解析开销可控。
	MaxTokenModelMappingBytes = 64 << 10
	// MaxTokenModelMappingNameLength 限制单个源/目标模型名的字节长度。
	MaxTokenModelMappingNameLength = 256
)

var ErrTokenModelMappingCycle = errors.New("token model mapping contains a cycle")

// GetModelMapping 返回存储的原始 JSON 字符串，未配置时为 ""。
func (token *Token) GetModelMapping() string {
	if token.ModelMapping == nil {
		return ""
	}
	return *token.ModelMapping
}

// GetModelMappingMap 宽松解析请求路径使用的映射：无法解析或解析后为空时返回 nil。
// 不做长度与环校验，脏数据由运行时解析器兜底。
func (token *Token) GetModelMappingMap() map[string]string {
	raw := strings.TrimSpace(token.GetModelMapping())
	if raw == "" || raw == "{}" {
		return nil
	}
	var parsed map[string]string
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return nil
	}
	mapping := make(map[string]string, len(parsed))
	for from, to := range parsed {
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			continue
		}
		mapping[from] = to
	}
	if len(mapping) == 0 {
		return nil
	}
	return mapping
}

// SetModelMapping 以规范化 JSON（键有序、紧凑）存储映射；空映射将字段清空为 NULL。
func (token *Token) SetModelMapping(mapping map[string]string) error {
	if len(mapping) == 0 {
		token.ModelMapping = nil
		return nil
	}
	data, err := common.Marshal(mapping)
	if err != nil {
		return err
	}
	token.ModelMapping = common.GetPointer(string(data))
	return nil
}

// ParseTokenModelMapping 严格解析并校验待保存的映射 JSON。
// 为空/`{}`/`null` 视为未配置并返回 nil；非法结构、非法名字、重复源模型或环形引用返回错误。
func ParseTokenModelMapping(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}
	if len(raw) > MaxTokenModelMappingBytes {
		return nil, fmt.Errorf("model redirect exceeds %d bytes", MaxTokenModelMappingBytes)
	}
	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return nil, errors.New("model redirect must be a JSON object")
	}
	normalized := make(map[string]string, len(parsed))
	for key, value := range parsed {
		to, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("value for model %q must be a string", key)
		}
		from := strings.TrimSpace(key)
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			return nil, errors.New("model name must not be empty")
		}
		for _, name := range []string{from, to} {
			if strings.ContainsFunc(name, unicode.IsControl) {
				return nil, fmt.Errorf("model name %q contains control characters", name)
			}
			if len(name) > MaxTokenModelMappingNameLength {
				return nil, fmt.Errorf("model name %q exceeds %d characters", name, MaxTokenModelMappingNameLength)
			}
		}
		if _, exists := normalized[from]; exists {
			return nil, fmt.Errorf("duplicate source model %q", from)
		}
		normalized[from] = to
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	for from := range normalized {
		if _, err := ResolveTokenModelMapping(normalized, from); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

// ResolveTokenModelMapping 沿映射链解析最终模型名，语义与渠道级 ModelMappedHelper 一致，
// 但只做精确 key 匹配（不回退 BaseModelName）。起点自引用视为未映射，链中自引用停在原节点，
// 真实环（A→B→A）返回 ErrTokenModelMappingCycle。未命中返回原名且无错。
func ResolveTokenModelMapping(mapping map[string]string, modelName string) (string, error) {
	if len(mapping) == 0 || modelName == "" {
		return modelName, nil
	}
	current := modelName
	visited := map[string]bool{current: true}
	for {
		next, ok := mapping[current]
		if !ok || next == "" {
			break
		}
		if visited[next] {
			if next == current {
				if current == modelName {
					return modelName, nil
				}
				break
			}
			return "", fmt.Errorf("%w: %s -> %s", ErrTokenModelMappingCycle, current, next)
		}
		visited[next] = true
		current = next
	}
	return current, nil
}

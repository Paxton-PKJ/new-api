package model

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	// ChannelRouteKeyPrefix 是渠道路由身份的固定前缀，便于在日志和配置里辨认。
	ChannelRouteKeyPrefix       = "ch_"
	channelRouteKeyRandomLength = 16
	// DirectRouteGroupPrefix 标记按渠道路由身份直接选择渠道的虚拟分组。它只存在于
	// 请求上下文与选择逻辑中，从不落库，也从不作为对外可见的分组名。
	DirectRouteGroupPrefix = "__route_"
)

var channelRouteKeyPattern = regexp.MustCompile(`^ch_[0-9A-Za-z]{16}$`)

// GenerateChannelRouteKey 生成一个新的渠道路由身份，随机源为 crypto/rand。
func GenerateChannelRouteKey() (string, error) {
	random, err := common.GenerateRandomCharsKey(channelRouteKeyRandomLength)
	if err != nil {
		return "", err
	}
	return ChannelRouteKeyPrefix + random, nil
}

func IsValidChannelRouteKey(key string) bool {
	return channelRouteKeyPattern.MatchString(key)
}

// RouteGroupName 返回渠道路由身份对应的虚拟分组名。
func RouteGroupName(routeKey string) string {
	return DirectRouteGroupPrefix + routeKey
}

// IsRouteGroup 判断分组名是否是虚拟路由分组。
func IsRouteGroup(group string) bool {
	return strings.HasPrefix(group, DirectRouteGroupPrefix)
}

// RouteKeyFromGroup 从虚拟路由分组名还原渠道路由身份，非虚拟分组或身份非法时返回 false。
func RouteKeyFromGroup(group string) (string, bool) {
	key, ok := strings.CutPrefix(group, DirectRouteGroupPrefix)
	if !ok || !IsValidChannelRouteKey(key) {
		return "", false
	}
	return key, true
}

// routeGroupChannel 把虚拟路由分组解析为唯一候选渠道；分组名非法、渠道不存在或
// 已停用时返回 nil。内存缓存与直查数据库两条路径对调用方透明。
func routeGroupChannel(group string) *Channel {
	key, ok := RouteKeyFromGroup(group)
	if !ok {
		return nil
	}
	channel, err := channelByRouteKey(key)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
		return nil
	}
	return channel
}

// channelByRouteKey 按路由身份取渠道，未知身份返回 (nil, nil)。
func channelByRouteKey(key string) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		var channel Channel
		err := DB.Where("route_key = ?", key).First(&channel).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &channel, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	id, ok := routeKey2channelID[key]
	if !ok {
		return nil, nil
	}
	return channelsIDM[id], nil
}

// routeGroupEnabledModels 返回虚拟路由分组对外的模型列表：启用渠道自己的模型，
// 去空后返回；渠道不存在或已停用时返回空切片。
func routeGroupEnabledModels(group string) []string {
	channel := routeGroupChannel(group)
	if channel == nil {
		return []string{}
	}
	models := channel.GetModels()
	return slices.DeleteFunc(models, func(model string) bool { return model == "" })
}

// resolveRouteGroupChannel 把虚拟路由分组解析为唯一候选渠道；渠道不存在、已停用、
// 不提供该模型或不满足请求过滤器时返回 nil，让自动分组循环继续下一项。
func resolveRouteGroupChannel(group, modelName string, filters []dto.ChannelFilter) *Channel {
	channel := routeGroupChannel(group)
	if channel == nil || !channelServesModel(channel, modelName) {
		return nil
	}
	if ok, _ := ChannelSatisfiesFilters(channel, modelName, filters); !ok {
		return nil
	}
	return channel
}

// channelServesModel 复刻 GetRandomSatisfiedChannel 的两段模型查找：先按请求的原名
// 匹配，再按归一化名匹配。
func channelServesModel(channel *Channel, modelName string) bool {
	models := channel.GetModels()
	if slices.Contains(models, modelName) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	return normalized != "" && slices.Contains(models, normalized)
}

func (channel *Channel) GetRouteKey() string {
	if channel.RouteKey == nil {
		return ""
	}
	return *channel.RouteKey
}

// ChannelRouteOption 是渠道路由身份的只读投影，不含密钥、模型与设置。
type ChannelRouteOption struct {
	Id       int     `json:"id"`
	RouteKey *string `json:"route_key"`
	Name     string  `json:"name"`
	Type     int     `json:"type"`
	Status   int     `json:"status"`
	Tag      *string `json:"tag"`
}

func ListChannelRouteOptions() ([]ChannelRouteOption, error) {
	var options []ChannelRouteOption
	err := DB.Model(&Channel{}).
		Select("id, route_key, name, type, status, tag").
		Order("id ASC").
		Find(&options).Error
	if err != nil {
		return nil, err
	}
	return options, nil
}

// ChannelsByRouteKeys 返回给定路由身份到渠道 id 的映射，未知身份不出现在结果里。
// 供保存路由预设时一次性校验引用的渠道是否存在。
func ChannelsByRouteKeys(keys []string) (map[string]int, error) {
	known := make(map[string]int, len(keys))
	if len(keys) == 0 {
		return known, nil
	}
	var channels []Channel
	if err := DB.Model(&Channel{}).
		Select("id, route_key").
		Where("route_key IN ?", keys).
		Find(&channels).Error; err != nil {
		return nil, err
	}
	for i := range channels {
		known[channels[i].GetRouteKey()] = channels[i].Id
	}
	return known, nil
}

// BackfillChannelRouteKeys 为缺少 route_key 的历史渠道补齐路由身份，
// 返回本次填充的行数。已有值的行不会被改动，重复调用是幂等的。
func BackfillChannelRouteKeys() (int, error) {
	var ids []int
	if err := DB.Model(&Channel{}).
		Where("route_key IS NULL OR route_key = ''").
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}

	filled := 0
	for _, id := range ids {
		key, err := GenerateChannelRouteKey()
		if err != nil {
			return filled, err
		}
		// 条件更新让并发或重复执行时只有第一个写入者生效。
		result := DB.Model(&Channel{}).
			Where("id = ? AND (route_key IS NULL OR route_key = '')", id).
			Update("route_key", key)
		if result.Error != nil {
			return filled, result.Error
		}
		filled += int(result.RowsAffected)
	}
	return filled, nil
}

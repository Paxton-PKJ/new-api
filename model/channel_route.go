package model

import (
	"regexp"

	"github.com/QuantumNous/new-api/common"
)

const (
	// ChannelRouteKeyPrefix 是渠道路由身份的固定前缀，便于在日志和配置里辨认。
	ChannelRouteKeyPrefix       = "ch_"
	channelRouteKeyRandomLength = 16
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

package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetChannelRouteOptions 返回渠道的稳定路由身份列表（只读），
// 供后续的路由预设按 route_key 直接引用渠道。
func GetChannelRouteOptions(c *gin.Context) {
	options, err := model.ListChannelRouteOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, options)
}

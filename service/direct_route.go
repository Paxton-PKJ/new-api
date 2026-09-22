package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ExposeSelectedGroup 记录本次选中的分组并返回对外可见的分组名：虚拟路由分组
// 映射为令牌所有者的用户分组（计费、日志、亲和统计都用它），同时把虚拟组名与
// route_key 留在上下文供 WS 重校验和管理员日志使用；普通分组原样写入并返回。
//
// 计费语义：直连路由按令牌所有者的用户分组计费，与"非 auto 的普通令牌"完全相同。
// 计费与倍率代码读到的 auto_group 就是用户分组，GetGroupGroupRatio(UserGroup,
// UserGroup) 特殊倍率照常生效；__route_ 前缀的分组名从不进入计费字段。
func ExposeSelectedGroup(c *gin.Context, group string) string {
	if !model.IsRouteGroup(group) {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, group)
		return group
	}
	visible := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	common.SetContextKey(c, constant.ContextKeyAutoGroup, visible)
	common.SetContextKey(c, constant.ContextKeySelectedRouteGroup, group)
	if key, ok := model.RouteKeyFromGroup(group); ok {
		common.SetContextKey(c, constant.ContextKeyRouteKey, key)
	}
	return visible
}

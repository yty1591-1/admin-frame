package main

import (
	"net/http"

	iamadmin "github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 A 端依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 管理端与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamRuntime) error {
	if rt != nil && rt.Handler != nil {
		iamadmin.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	return nil
}

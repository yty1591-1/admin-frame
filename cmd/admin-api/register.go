package main

import (
	"net/http"

	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	iamadmin "github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	identityadmin "github.com/admin-frame/admin-frame/internal/identity/transport/admin"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 A 端依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 管理端与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
// identityRT 用于注册身份域（登录 / 刷新 / 登出 / 会话管理）路由。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamRuntime, identityRT *identityruntime.Runtime) error {
	if rt != nil && rt.Handler != nil {
		iamadmin.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	if identityRT != nil && identityRT.Handler != nil {
		identityadmin.Register(mux, identityRT.Handler, deps.AuthMW)
	}
	return nil
}

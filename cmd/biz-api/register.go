package main

import (
	"net/http"

	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	iambiz "github.com/admin-frame/admin-frame/internal/iam/transport/biz"
	identitybiz "github.com/admin-frame/admin-frame/internal/identity/transport/biz"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 B 端依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 自助管理与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
// identityRT 用于注册身份域（登录 / 刷新 / 登出）路由。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamruntime.Runtime, identityRT *identityruntime.Runtime) error {
	if rt != nil && rt.Handler != nil {
		iambiz.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	if identityRT != nil && identityRT.Handler != nil {
		identitybiz.Register(mux, identityRT.Handler, deps.AuthMW)
	}
	return nil
}

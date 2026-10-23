package main

import (
	"net/http"

	identityruntime "github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	identityc "github.com/admin-frame/admin-frame/internal/identity/transport/c"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 C 端业务依赖并注册本端路由。
//
// C 端当前仅有身份认证与平台 probe 端点；新增业务资源时需同时补权限点、
// OpenAPI 契约与 TEST-03 用户归属用例。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, identityRT *identityruntime.Runtime) error {
	if identityRT != nil && identityRT.CHandler != nil {
		identityc.Register(mux, identityRT.CHandler, deps.AuthMW)
	}
	return nil
}

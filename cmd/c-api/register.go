package main

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 C 端业务依赖并注册本端路由。
//
// C 端当前仅有平台 probe 端点，暂无业务资源；新增资源时需同时补权限点、
// OpenAPI 契约与 TEST-03 用户归属用例。
func registerRoutes(_ *platform.Deps, _ *http.ServeMux) error {
	return nil
}

package main

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order"
	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	biztransport "github.com/admin-frame/admin-frame/internal/order/transport/biz"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// newAuthorizer 构造 B 端演示权限表（merchant 隔离仍由 Handler/app 强制）。
//
// 生产环境应改为从 IAM 拉取角色权限，接口保持不变。
func newAuthorizer() *security.Authorizer {
	checker := rbac.New(rbac.WithSuperRoles("admin"))
	checker.GrantRole("merchant_staff", order.PermRead, order.PermWrite)
	return security.NewAuthorizer(checker)
}

// registerRoutes 装配 B 端业务依赖并注册本端路由。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux) error {
	repo := persistence.NewMemoryRepository()
	deps.Health.Register("memory_repo", repo.Ping)
	bus := eventbus.NewMemoryBus(deps.Logger)
	svc := app.NewOrderService(repo, bus)
	mux.Handle("/", biztransport.NewRouter(biztransport.NewHandler(svc), deps.AuthMW, deps.RequirePermission))
	return nil
}

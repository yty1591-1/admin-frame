package main

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/files"
	filesapp "github.com/admin-frame/admin-frame/internal/files/app"
	filesadmin "github.com/admin-frame/admin-frame/internal/files/transport/admin"
	"github.com/admin-frame/admin-frame/internal/order"
	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	admintransport "github.com/admin-frame/admin-frame/internal/order/transport/admin"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// newAuthorizer 构造 A 端演示权限表。
//
// 真实环境应由 IAM 返回角色/权限点并在此注入同一 rbac.Checker 接口；
// 角色与权限点属于业务契约，不放进 platform（ARCHITECTURE §5.6）。
func newAuthorizer() *security.Authorizer {
	checker := rbac.New(rbac.WithSuperRoles("admin"))
	checker.GrantRole("ops",
		files.PermRead, files.PermWrite,
		order.PermRead, order.PermWrite,
	)
	return security.NewAuthorizer(checker)
}

// registerRoutes 装配 A 端业务依赖并注册本端路由。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux) error {
	repo := persistence.NewMemoryRepository()
	deps.Health.Register("memory_repo", repo.Ping)
	bus := eventbus.NewMemoryBus(deps.Logger)
	orderSvc := app.NewOrderService(repo, bus)
	admintransport.Register(mux, admintransport.NewHandler(orderSvc), deps.AuthMW, deps.RequirePermission)

	fileSvc := filesapp.NewService(deps.Storage.Default, filesapp.Config{
		MaxBytes:      deps.Config.Storage.MaxObjectBytes,
		PresignPutTTL: deps.Config.Storage.PresignPutTTL,
		PresignGetTTL: deps.Config.Storage.PresignGetTTL,
	})
	filesadmin.Register(mux, filesadmin.NewHandler(fileSvc), deps.AuthMW, deps.RequirePermission)
	return nil
}

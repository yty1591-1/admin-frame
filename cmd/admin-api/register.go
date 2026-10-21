package main

import (
	"net/http"

	filesapp "github.com/admin-frame/admin-frame/internal/files/app"
	filesadmin "github.com/admin-frame/admin-frame/internal/files/transport/admin"
	iamadmin "github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	admintransport "github.com/admin-frame/admin-frame/internal/order/transport/admin"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 A 端业务依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 管理端与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamRuntime) error {
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

	if rt != nil && rt.Handler != nil {
		iamadmin.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	return nil
}

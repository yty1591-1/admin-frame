package main

import (
	"net/http"

	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	iambiz "github.com/admin-frame/admin-frame/internal/iam/transport/biz"
	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	biztransport "github.com/admin-frame/admin-frame/internal/order/transport/biz"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 B 端业务依赖并注册本端路由。
//
// rt 由 NewAuthorizer 阶段构造并复用：IAM 自助管理与鉴权中间件必须看到同一份
// 角色 / 绑定数据，否则本地内存模式下会出现「刚授权的角色不生效」。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux, rt *iamruntime.Runtime) error {
	repo := persistence.NewMemoryRepository()
	deps.Health.Register("memory_repo", repo.Ping)
	bus := eventbus.NewMemoryBus(deps.Logger)
	svc := app.NewOrderService(repo, bus)
	mux.Handle("/", biztransport.NewRouter(biztransport.NewHandler(svc), deps.AuthMW, deps.RequirePermission))

	if rt != nil && rt.Handler != nil {
		iambiz.Register(mux, rt.Handler, deps.AuthMW, deps.RequirePermission)
	}
	return nil
}

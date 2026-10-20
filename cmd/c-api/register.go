package main

import (
	"net/http"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	ctransport "github.com/admin-frame/admin-frame/internal/order/transport/c"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// registerRoutes 装配 C 端业务依赖并注册本端路由。
//
// C 端当前未使用权限点：只做身份校验，资源归属由 Handler/app 强制。
func registerRoutes(deps *platform.Deps, mux *http.ServeMux) error {
	repo := persistence.NewMemoryRepository()
	deps.Health.Register("memory_repo", repo.Ping)
	bus := eventbus.NewMemoryBus(deps.Logger)
	svc := app.NewOrderService(repo, bus)
	mux.Handle("/", ctransport.NewRouter(ctransport.NewHandler(svc), deps.AuthMW))
	return nil
}

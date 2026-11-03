// Package tenantruntime 装配租户域（ENT-09）。
//
// 装配职责：按数据库可用性选择仓储实现，注入审计出口与 ID 生成器，
// 构造 A 端 Handler。租户域没有配置开关：它是隔离边界，缺它则 B 端
// 无法回答「这个商户是否还在服务期」。
package tenantruntime

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
	tenantrest "github.com/admin-frame/admin-frame/internal/tenant/transport/rest"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// 分页基线：与 A 端其它管理列表保持一致（page_size 上限 100）。
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// Runtime 是租户域的运行时产物。
type Runtime struct {
	// Handler A 端租户管理 Handler；为 nil 表示本进程未启用（缺配置或依赖）。
	Handler *tenantrest.Handler
}

// New 构造租户运行时。
//
// 数据库可用时用 MySQL 仓储并把审计写入审计表；内存模式（local / dev / test）
// 用内存仓储 + 结构化日志审计，保证本地开发不需要起库就能走通生命周期。
func New(deps *platform.Deps) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return &Runtime{}, nil
	}

	var (
		repo    domain.Repository
		emitter audit.Emitter = audit.SlogEmitter{Log: deps.Logger}
	)
	if deps.Database != nil {
		mysqlRepo, err := persistence.NewMySQLRepository(deps.Database.SQL())
		if err != nil {
			return nil, fmt.Errorf("tenant repository: %w", err)
		}
		repo = mysqlRepo
		// 双出口：审计表供查询 / 取证，结构化日志供排障；任一失败不影响另一出口。
		emitter = audit.MultiEmitter{
			audit.SQLEmitter{DB: deps.Database.SQL()},
			audit.SlogEmitter{Log: deps.Logger},
		}
	} else {
		repo = persistence.NewMemoryRepository()
	}

	service, err := app.NewService(repo, deps.IDs, emitter, app.Options{
		DefaultPageSize: defaultPageSize,
		MaxPageSize:     maxPageSize,
		Service:         deps.Config.Service,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant service: %w", err)
	}

	handler, err := tenantrest.NewHandler(tenantrest.HandlerConfig{
		Service: service,
		PageLimits: pagination.Config{
			DefaultPageSize: defaultPageSize,
			MaxPageSize:     maxPageSize,
		},
		Channel: deps.Config.Channel,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant handler: %w", err)
	}
	return &Runtime{Handler: handler}, nil
}

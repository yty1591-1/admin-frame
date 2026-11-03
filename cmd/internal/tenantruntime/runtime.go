// Package tenantruntime 装配租户域（ENT-09）。
//
// 装配职责：按数据库可用性选择仓储实现，注入审计出口与 ID 生成器，
// 构造 A 端 Handler 与 B 端状态守卫。租户域没有配置开关：它是隔离边界，
// 缺它则 B 端无法回答「这个商户是否还在服务期」。
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

// New 构造租户运行时（A 端管理能力）。
func New(deps *platform.Deps) (*Runtime, error) {
	if deps == nil || deps.Config == nil || deps.IDs == nil {
		return &Runtime{}, nil
	}

	store, emitter, err := buildDeps(deps)
	if err != nil {
		return nil, err
	}
	repo := store.tenants

	service, err := app.NewService(repo, deps.IDs, emitter, app.Options{
		DefaultPageSize: defaultPageSize,
		MaxPageSize:     maxPageSize,
		Service:         deps.Config.Service,
	})
	if err != nil {
		return nil, fmt.Errorf("tenant service: %w", err)
	}

	memberService, err := app.NewMemberService(store.tenants, store.members, emitter, app.Options{Service: deps.Config.Service})
	if err != nil {
		return nil, fmt.Errorf("tenant member service: %w", err)
	}
	quotaService, err := app.NewQuotaService(store.tenants, store.quotas, emitter, app.Options{Service: deps.Config.Service})
	if err != nil {
		return nil, fmt.Errorf("tenant quota service: %w", err)
	}

	handler, err := tenantrest.NewHandler(tenantrest.HandlerConfig{
		Service: service,
		Members: memberService,
		Quotas:  quotaService,
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

// NewGuard 构造 B 端租户状态守卫（platform.TenantGuard 实现）。
//
// 内存模式（db.driver=memory）返回 nil 表示不启用：内存仓储是进程私有的，
// 与 A 端进程不共享租户表，用它判定会把所有 B 端请求误判为「租户不存在」。
// 生产与集成环境使用 MySQL，守卫必然启用。
func NewGuard(deps *platform.Deps) (platform.TenantGuard, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	store, _, err := buildDeps(deps)
	if err != nil {
		return nil, err
	}
	guard, err := app.NewGuard(store.tenants, app.GuardOptions{})
	if err != nil {
		return nil, fmt.Errorf("tenant guard: %w", err)
	}
	return guard, nil
}

// store 把三组仓储接口打包，便于在同一装配点复用同一份实现。
//
// 内存与 SQL 实现都同时满足三者（methods 命名刻意加 Member/Quota 前缀，
// 避免与租户的 Get/List 签名冲突）。
type store struct {
	tenants domain.Repository
	members domain.MemberRepository
	quotas  domain.QuotaRepository
}

// buildDeps 按数据库可用性选择仓储与审计出口。
//
// 数据库可用时用 MySQL 仓储并把审计写入审计表；内存模式用内存仓储 +
// 结构化日志审计，保证本地开发不需要起库就能走通生命周期。
func buildDeps(deps *platform.Deps) (store, audit.Emitter, error) {
	emitter := audit.Emitter(audit.SlogEmitter{Log: deps.Logger})
	if deps.Database == nil {
		memory := persistence.NewMemoryRepository()
		return store{tenants: memory, members: memory, quotas: memory}, emitter, nil
	}
	mysqlRepo, err := persistence.NewMySQLRepository(deps.Database.SQL())
	if err != nil {
		return store{}, nil, fmt.Errorf("tenant repository: %w", err)
	}
	// 双出口：审计表供查询 / 取证，结构化日志供排障；任一失败不影响另一出口。
	return store{tenants: mysqlRepo, members: mysqlRepo, quotas: mysqlRepo}, audit.MultiEmitter{
		audit.SQLEmitter{DB: deps.Database.SQL()},
		audit.SlogEmitter{Log: deps.Logger},
	}, nil
}

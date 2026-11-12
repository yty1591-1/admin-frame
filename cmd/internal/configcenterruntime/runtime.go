// Package configcenterruntime 装配配置中心运行时产物（M1-3）。
//
// 装配语义与 featureflag / tenant runtime 一致：
//   - 内存 / MySQL 仓储按 db.driver 切换（MySQL 缺失时退化为进程内内存仓储）；
//   - 管理面（ItemService + ReleaseService + Handler）供 admin-api 注册；
//   - 启动时按代码 key spec 对每个 namespace 幂等播种 baseline Release；
//   - 审计事件与业务事务同源（Outbox），失败即启动失败（fail closed）。
package configcenterruntime

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	configcenterrest "github.com/admin-frame/admin-frame/internal/configcenter/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// 分页基线：与 A 端其它管理列表保持一致（page_size 上限 100）。
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// 启动播种主体：系统身份，不冒用任何人类账号。
const (
	bootstrapSubject = "system:config-bootstrap"
	bootstrapTrace   = "config-bootstrap"
)

// Runtime 是配置中心域的运行时产物。
type Runtime struct {
	// Handler A 端管理 Handler；为 nil 表示本进程未装配（缺平台依赖）。
	Handler *configcenterrest.Handler
}

// Options 是装配参数。
type Options struct {
	// Service 审计服务名；空时取 deps.Config.Service。
	Service string
}

// repositories 汇总三个持久化端口；内存与 MySQL 实现同签名。
type repositories struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
}

// New 装配配置中心运行时。
//
// 依赖缺失（deps / Config / IDs）返回装配错误，由调用方决定是否阻断本进程
// （admin-api 与 featureflag 同口径：记录告警并关闭本域路由）。
func New(deps *platform.Deps, opts Options) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("configcenter runtime requires platform deps")
	}
	if deps.IDs == nil {
		return nil, fmt.Errorf("configcenter runtime requires id generator")
	}

	repos, sqlDB, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}
	emitter := buildEmitter(deps)
	service := opts.Service
	if service == "" {
		service = deps.Config.Service
	}

	// M1-4 才回填全量键声明；当前为空注册表，因此 Namespaces() 为空、
	// 启动播种自然 no-op。管理面写入未声明键会被 400 拒绝（fail closed）。
	registry := spec.NewRegistry()

	itemService, err := app.NewItemService(repos.items, registry, emitter, deps.IDs,
		app.Options{Service: service})
	if err != nil {
		return nil, fmt.Errorf("configcenter item service: %w", err)
	}
	releaseService, err := app.NewReleaseService(repos.items, repos.releases, repos.pointers,
		emitter, deps.IDs, app.Options{Service: service})
	if err != nil {
		return nil, fmt.Errorf("configcenter release service: %w", err)
	}
	bootstrapService, err := app.NewBootstrapService(repos.items, repos.releases, repos.pointers,
		registry, emitter, deps.IDs, app.Options{Service: service})
	if err != nil {
		return nil, fmt.Errorf("configcenter bootstrap service: %w", err)
	}
	if err := bootstrap(deps, bootstrapService, registry, sqlDB); err != nil {
		return nil, err
	}

	handler, err := configcenterrest.NewHandler(configcenterrest.HandlerConfig{
		Items:    itemService,
		Releases: releaseService,
		Specs:    registry,
		PageLimits: pagination.Config{
			DefaultPageSize: defaultPageSize,
			MaxPageSize:     maxPageSize,
		},
		SQLDB: sqlDB,
	})
	if err != nil {
		return nil, fmt.Errorf("configcenter handler: %w", err)
	}
	return &Runtime{Handler: handler}, nil
}

// bootstrap 对每个已声明 namespace 幂等播种 baseline；任一失败即返回（fail fast）。
//
// 每个 namespace 单独一个事务：一个 namespace 失败不会留下半状态，也不会
// 回滚其它已成功的 namespace（播种本身幂等，重启可继续）。
func bootstrap(deps *platform.Deps, service *app.BootstrapService,
	registry *spec.Registry, sqlDB *sql.DB) error {
	bootstrapActor := app.Actor{
		SubjectID: bootstrapSubject,
		ActorType: "system",
		Channel:   "admin",
		TraceID:   bootstrapTrace,
	}
	for _, namespace := range registry.Namespaces() {
		if err := withinTx(context.Background(), sqlDB, func(ctx context.Context) error {
			_, ensureErr := service.EnsureBaseline(ctx, app.BootstrapInput{
				Env:       deps.Config.Env,
				Namespace: namespace,
				Actor:     bootstrapActor,
			})
			return ensureErr
		}); err != nil {
			return fmt.Errorf("configcenter bootstrap namespace %q: %w", namespace, err)
		}
	}
	return nil
}

// withinTx 在 SQL 模式下包事务；内存模式直接执行。
func withinTx(ctx context.Context, sqlDB *sql.DB, fn func(context.Context) error) error {
	if sqlDB == nil {
		return fn(ctx)
	}
	return db.WithinUnitOfWork(ctx, sqlDB, nil, fn)
}

// buildRepositories 内存 / MySQL 切换（对齐 tenantruntime 口径）。
func buildRepositories(deps *platform.Deps) (repositories, *sql.DB, error) {
	if deps.Database == nil {
		memoryRepos := memory.New()
		return repositories{
			items:    memoryRepos.Items,
			releases: memoryRepos.Releases,
			pointers: memoryRepos.Pointers,
		}, nil, nil
	}
	sqlDB := deps.Database.SQL()
	repos, err := persistence.New(sqlDB)
	if err != nil {
		return repositories{}, nil, fmt.Errorf("configcenter repository: %w", err)
	}
	return repositories{
		items:    repos.Items,
		releases: repos.Releases,
		pointers: repos.Pointers,
	}, sqlDB, nil
}

// buildEmitter 审计出口：优先 Outbox（与业务事务同源），退回双出口
// （审计表 + 结构化日志），内存模式退回结构化日志。
func buildEmitter(deps *platform.Deps) audit.Emitter {
	slogEmitter := platformaudit.SlogEmitter{Log: deps.Logger}
	if deps.Async != nil && deps.Async.Store() != nil {
		return platformaudit.OutboxEmitter{Topic: "configcenter.audit", Events: deps.Async.Store()}
	}
	if deps.Database != nil {
		return platformaudit.MultiEmitter{
			platformaudit.SQLEmitter{DB: deps.Database.SQL()},
			slogEmitter,
		}
	}
	return slogEmitter
}

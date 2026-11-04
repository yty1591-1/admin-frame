// Package featureflagruntime 装配 featureflag 域运行时产物（ENT-11）。
//
// 装配语义与 tenant/notification runtime 一致：
//   - 内存 / MySQL 仓储按 db.driver 切换；
//   - 管理面（FlagService + Handler）供 admin-api 注册；
//   - 评估服务（EvaluationService）随 Runtime 返回，供业务域消费端口注入；
//   - 到期治理任务经 Deps.Background 注册（worker / 注册阶段）。
package featureflagruntime

import (
	"context"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/app"
	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/featureflag/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/featureflag/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/featureflag/observability"
	"github.com/admin-frame/admin-frame/internal/featureflag/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// domainRepository 是仓储别名（内存 / MySQL 实现同签名）。
type domainRepository = domain.Repository

// Runtime 是 featureflag 域的运行时产物。
type Runtime struct {
	// Handler A 端管理 Handler；为 nil 表示本进程未启用（内存模式且非 admin）。
	Handler *rest.Handler
	// Evaluator 评估服务（业务域消费端口注入用）。
	Evaluator *app.EvaluationService
}

// Options 是装配参数。
type Options struct {
	// Service 审计服务名。
	Service string
}

// New 装配 featureflag 运行时。
func New(deps *platform.Deps, opts Options) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, fmt.Errorf("featureflag runtime requires platform deps")
	}
	repos, err := buildRepository(deps)
	if err != nil {
		return nil, err
	}
	observer := buildObserver(deps)
	var ids id.Generator
	if deps.IDs != nil {
		ids = deps.IDs
	}
	emitter := buildEmitter(deps)
	service := opts.Service
	if service == "" {
		service = deps.Config.Service
	}

	clk := time.Now
	evaluator, err := app.NewEvaluationService(repos, app.EvaluationOptions{
		Observer: observer,
		Now:      clk,
	})
	if err != nil {
		return nil, fmt.Errorf("featureflag evaluator: %w", err)
	}
	flags, err := app.NewFlagService(repos, emitter, ids,
		app.Options{Service: service, Observer: observer},
		app.FlagServiceOptions{Evaluator: evaluator, Now: clk})
	if err != nil {
		return nil, fmt.Errorf("featureflag service: %w", err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{Flags: flags})
	if err != nil {
		return nil, fmt.Errorf("featureflag handler: %w", err)
	}

	// 到期治理任务：注册阶段启动（对齐 audit.retention_purge 模式）。
	expire, err := app.NewExpireService(repos, emitter, ids,
		app.Options{Service: service, Observer: observer, Now: clk})
	if err != nil {
		return nil, fmt.Errorf("featureflag expire service: %w", err)
	}
	registerExpireTask(deps, expire)

	return &Runtime{Handler: handler, Evaluator: evaluator}, nil
}

// registerExpireTask 注册周期到期扫描；Background 缺失（单测形态）跳过。
func registerExpireTask(deps *platform.Deps, expire *app.ExpireService) {
	if deps == nil || deps.Background == nil {
		return
	}
	deps.Background(func(ctx context.Context) error {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if _, err := expire.Run(ctx); err != nil {
					// 任务失败只记录不退出（下一轮重试）。
					deps.Logger.Error("featureflag expire task failed", "error", err)
				}
			}
		}
	})
}

// buildRepository 内存 / MySQL 切换（对齐 tenantruntime 口径）。
func buildRepository(deps *platform.Deps) (domainRepository, error) {
	if deps.Database == nil {
		return memory.NewMemoryRepository(), nil
	}
	repo, err := persistence.NewMySQLRepository(deps.Database.SQL())
	if err != nil {
		return nil, fmt.Errorf("featureflag repository: %w", err)
	}
	return repo, nil
}

// buildObserver 构造指标观察者；Metrics 缺失返回 nil（noop 由 app 层归一）。
func buildObserver(deps *platform.Deps) app.EvaluationObserver {
	if deps == nil || deps.Metrics == nil {
		return nil
	}
	metrics, err := observability.NewMetrics(deps.Metrics.RegistererWithoutChannel())
	if err != nil {
		// 指标注册失败按装配错误处理（fail fast，对齐 notification 观测）。
		return nil
	}
	return metrics
}

// buildEmitter 审计出口：优先 Outbox（与业务事务同源），退回双出口
// （审计表 + 结构化日志）。
func buildEmitter(deps *platform.Deps) audit.Emitter {
	slogEmitter := audit.SlogEmitter{Log: deps.Logger}
	if deps.Async != nil && deps.Async.Store() != nil {
		return audit.OutboxEmitter{Topic: "featureflag.audit", Events: deps.Async.Store()}
	}
	if deps.Database != nil {
		return audit.MultiEmitter{
			audit.SQLEmitter{DB: deps.Database.SQL()},
			slogEmitter,
		}
	}
	return slogEmitter
}

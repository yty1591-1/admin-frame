// Package iamruntime 负责 IAM 在 admin-api / biz-api 进程内的统一装配。
//
// 装配顺序固定为：仓储 → 缓存 → 用例 → 鉴权器 → HTTP Handler。
// 两个端必须共享同一份实例，否则「管理端写入」与「鉴权读取」会落在不同内存副本上。
//
// 本包位于 cmd/internal 下：只允许 cmd 下的进程入口引用，业务域不得依赖装配细节。
package iamruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/iam/observability"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// Runtime 是 IAM 在本进程内的唯一实例集合。
type Runtime struct {
	// Authorizer 供 platform 构造权限中间件。
	Authorizer *security.Authorizer
	// Handler 供各端 transport 注册 HTTP 路由；mode=off 时仍存在，便于先建授权。
	Handler *rest.Handler
}

// Options 是 IAM 运行时装配参数。
type Options struct {
	// StaticChecker 是 mode=off 的静态回滚表，必须由端别提供：
	// A 端与 B 端的演示角色不同，不能共用一张表。
	StaticChecker *rbac.Checker
}

// New 按配置装配 IAM：仓储 → 缓存 → 用例 → 鉴权器。
//
// mode=off 时仍装配管理端（便于影子期先建角色与绑定），但鉴权保持静态表。
func New(deps *platform.Deps, opts Options) (*Runtime, error) {
	cfg := deps.Config
	mode, err := security.ParseMode(cfg.IAM.Mode)
	if err != nil {
		return nil, err
	}
	staticChecker := opts.StaticChecker
	if staticChecker == nil {
		// 未注入静态表时按默认拒绝处理，绝不回落到「全部放行」。
		staticChecker = rbac.New()
	}
	if cfg.Channel != "admin" && cfg.Channel != "biz" {
		// C 端 / worker 不提供 IAM；配置校验已拒绝非 off 模式，这里再做一次兜底。
		return &Runtime{Authorizer: security.NewAuthorizer(staticChecker)}, nil
	}

	roles, bindings, snapshots, err := buildRepositories(deps)
	if err != nil {
		return nil, err
	}

	snapshotCache, err := buildSnapshotCache(deps)
	if err != nil {
		return nil, err
	}
	registerHealth(deps)

	iamMetrics, err := observability.NewMetrics(deps.Metrics.Registerer())
	if err != nil {
		return nil, fmt.Errorf("iam metrics: %w", err)
	}
	if err := iamMetrics.RegisterBreakglassGauge(breakglassActiveCounter(roles, bindings)); err != nil {
		return nil, fmt.Errorf("iam breakglass gauge: %w", err)
	}

	authorizeSvc, err := app.NewAuthorizeService(app.AuthorizeConfig{
		Repository:     snapshots,
		Cache:          snapshotCache,
		Env:            cfg.Env,
		LoadTimeout:    cfg.IAM.LoadTimeout,
		MaxRoles:       cfg.IAM.Limits.MaxRolesPerSubject,
		MaxPermissions: cfg.IAM.Limits.MaxPermissionsPerRole,
		AllowWildcard:  cfg.IAM.Security.AllowWildcard,
		Observer:       iamMetrics,
	})
	if err != nil {
		return nil, fmt.Errorf("iam authorize service: %w", err)
	}

	ids := id.MustSnowflake(id.Options{NodeID: 1})
	recorder := observability.ChangeRecorder{
		Inner:   app.SlogRecorder{Emitter: audit.SlogEmitter{Log: deps.Logger}},
		Metrics: iamMetrics,
	}
	auditMeta := app.AuditMeta{Service: cfg.Service, ActorType: auditActorType(cfg.Channel)}
	invalidator := iampcache.NewSubjectInvalidator(snapshotCache, cfg.Env)

	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: invalidator,
		IDs:         ids,
		Audit:       auditMeta,
		Limits: app.RoleLimits{
			MaxPermissionsPerRole:     cfg.IAM.Limits.MaxPermissionsPerRole,
			MaxCustomRolesPerMerchant: cfg.IAM.Limits.MaxCustomRolesPerMerchant,
			RequireReason:             cfg.IAM.Security.RequireReason,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam role service: %w", err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: invalidator,
		IDs:         ids,
		Audit:       auditMeta,
		Limits: app.BindingLimits{
			MaxBindingsPerSubject: cfg.IAM.Limits.MaxBindingsPerSubject,
			RequireReason:         cfg.IAM.Security.RequireReason,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam binding service: %w", err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, snapshots)
	if err != nil {
		return nil, fmt.Errorf("iam query service: %w", err)
	}

	applicator, err := app.NewRoleChangeApplicator(roleSvc, bindingSvc)
	if err != nil {
		return nil, fmt.Errorf("iam change applicator: %w", err)
	}
	changeSvc, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   buildChangeRequestRepository(deps),
		Applicator: applicator,
		IDs:        ids,
		Limits: app.ChangeLimits{
			RequireReason: cfg.IAM.Security.RequireReason,
			// B 端审批需要 iam_change_request.merchant_id 列，待 000004 迁移后开启。
			AllowBizChannel: false,
			AllowBreakglass: cfg.IAM.Breakglass.Enabled,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam change request service: %w", err)
	}
	breakglassSvc, err := app.NewBreakglassService(app.BreakglassServiceConfig{
		Changes:  changeSvc,
		Recorder: recorder,
		IDs:      ids,
		Audit:    auditMeta,
		Limits: app.BreakglassLimits{
			Enabled:         cfg.IAM.Breakglass.Enabled,
			AllowedSubjects: cfg.IAM.Breakglass.AllowedSubjects,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam breakglass service: %w", err)
	}

	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles:      roleSvc,
		Bindings:   bindingSvc,
		Query:      querySvc,
		Changes:    changeSvc,
		Breakglass: breakglassSvc,
		Channel:    domain.Channel(cfg.Channel),
	})
	if err != nil {
		return nil, fmt.Errorf("iam handler: %w", err)
	}

	authorizer := security.NewAuthorizer(staticChecker)
	if mode != security.ModeOff {
		authorizer = security.NewAuthorizer(staticChecker,
			security.WithSnapshotLoader(authorizeSvc),
			security.WithMode(mode),
			security.WithShadowObserver(iamMetrics),
			security.WithDecisionObserver(iamMetrics),
		)
	}
	return &Runtime{Authorizer: authorizer, Handler: handler}, nil
}

// breakglassActiveCounter 统计当前有效破玻璃绑定数；角色未播种时返回 0。
//
// 不引入额外仓储方法：复用 ListByRole 后按 expires_at 过滤，内存 / MySQL 语义一致。
func breakglassActiveCounter(
	roles domain.RoleRepository,
	bindings domain.BindingRepository,
) func() (int, error) {
	return func() (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		role, err := roles.FindByCode(ctx, domain.ChannelAdmin, "", domain.BreakglassRoleCode)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return 0, nil
			}
			return 0, err
		}
		if role.IsDeleted() || role.Status != domain.RoleStatusActive {
			// 角色被停用 / 软删后绑定不再展开 "*"，指标必须与鉴权语义一致。
			return 0, nil
		}
		list, err := bindings.ListByRole(ctx, role.ID)
		if err != nil {
			return 0, err
		}
		now := time.Now().UTC()
		active := 0
		for _, binding := range list {
			if !binding.IsExpired(now) {
				active++
			}
		}
		return active, nil
	}
}

// buildRepositories 按 db.driver 选择内存或 MySQL 仓储。
func buildRepositories(deps *platform.Deps) (domain.RoleRepository, domain.BindingRepository, domain.SnapshotRepository, error) {
	if deps.Database == nil {
		roles := persistence.NewMemoryRoleRepository()
		bindings := persistence.NewMemoryBindingRepository()
		return roles, bindings, persistence.NewMemorySnapshotRepository(roles, bindings), nil
	}
	sqlDB := deps.Database.SQL()
	return persistence.NewSQLRoleRepository(sqlDB),
		persistence.NewSQLBindingRepository(sqlDB),
		persistence.NewSQLSnapshotRepository(sqlDB),
		nil
}

// buildChangeRequestRepository 按 db.driver 选择审批单仓储。
//
// 审批单必须持久化：进程重启丢失 pending 单会让「已批准未应用」的变更既不被执行
// 也无法审计，因此生产使用 SQL 实现，内存实现只用于 local / demo / 单测。
func buildChangeRequestRepository(deps *platform.Deps) domain.ChangeRequestRepository {
	if deps.Database == nil {
		return persistence.NewMemoryChangeRequestRepository()
	}
	return persistence.NewSQLChangeRequestRepository(deps.Database.SQL())
}

// registerHealth 把 IAM 依赖注册到 /readyz（OBS-03）。
//
// 只注册真实外部依赖：db.driver=memory 或未配置 Redis 时不注册，
// 避免就绪探针报告永远成功的假检查（local/demo 不应掩盖依赖缺失）。
func registerHealth(deps *platform.Deps) {
	if deps == nil || deps.Health == nil {
		return
	}
	if deps.Database != nil {
		deps.Health.Register("iam_db", deps.Database.Checker())
	}
	if deps.Redis != nil {
		deps.Health.Register("iam_cache", deps.Redis.Checker())
	}
}

// buildSnapshotCache 装配 L1（进程内）+ 可选 L2（Redis）。
func buildSnapshotCache(deps *platform.Deps) (*iampcache.SnapshotCache, error) {
	cfg := deps.Config.IAM.Cache
	var l2 pkgcache.Store
	if deps.Redis != nil {
		l2 = iampcache.NewRedisStore(deps.Redis.Cache())
	}
	if cfg.Driver == platform.IAMCacheDriverRedis && l2 == nil {
		return nil, apperr.Internal("iam cache driver=redis but redis is not configured", nil)
	}
	layers := iampcache.Config{
		L1:          pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: cfg.L1MaxEntries}),
		L2:          l2,
		L1TTL:       cfg.L1TTL,
		L2TTL:       cfg.L2TTL,
		NegativeTTL: cfg.NegativeTTL,
	}
	if cfg.Driver == platform.IAMCacheDriverMemory {
		layers.L2 = nil
	}
	return iampcache.NewSnapshotCache(layers)
}

// auditActorType 返回审计事件的操作人类型。
func auditActorType(channel string) string {
	if channel == "biz" {
		return "merchant_staff"
	}
	return "staff"
}

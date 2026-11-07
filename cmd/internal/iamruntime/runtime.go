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
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/iam/menuview"
	"github.com/admin-frame/admin-frame/internal/iam/observability"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	pkgcache "github.com/admin-frame/admin-frame/pkg/cache"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// defaultApprovalTTL 是审批单的默认决策时效。
//
// 审批单承载的是「某一时刻的变更意图」：载荷与目标聚合都可能随时间变化，
// 超过该时限仍未决策的单据按 expired 处理，必须重新发起，避免迟到的批准
// 授权了已经过期的意图。
const defaultApprovalTTL = 72 * time.Hour

// configView 把平台段配置与 IAM 域配置组合为只读视图，供装配期各构造器使用。
type configView struct {
	*platform.Config
	IAM iamconfig.IAMConfig
}

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
func New(deps *platform.Deps, iamCfg iamconfig.IAMConfig, opts Options) (*Runtime, error) {
	if deps == nil || deps.Config == nil {
		return nil, apperr.Internal("iam runtime requires platform deps", nil)
	}
	cfg := configView{Config: deps.Config, IAM: iamCfg}
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

	roles, bindings, templates, snapshots, err := BuildRepositories(deps)
	if err != nil {
		return nil, err
	}
	sodGroups, roleRestrictions := buildConstraintRepositories(deps)
	sodViolations := buildSodViolationRepository(deps, roles, bindings)
	constraintMode, err := app.ParseConstraintMode(cfg.IAM.Constraints.Mode)
	if err != nil {
		return nil, err
	}

	registerHealth(deps)

	// IAM 指标自带 channel 变量标签，必须使用不注入 channel 常量标签的注册器，
	// 否则 Prometheus 会以「常量标签与变量标签重名」拒绝注册导致进程启动失败。
	iamMetrics, err := observability.NewMetrics(deps.Metrics.RegistererWithoutChannel())
	if err != nil {
		return nil, fmt.Errorf("iam metrics: %w", err)
	}
	// 快照缓存必须带上观测器：否则 iam_authz_cache_total 恒为空，
	// 看板「快照缓存命中」面板永远没有数据（ENT-06 T6 实测）。
	snapshotCache, err := buildSnapshotCache(deps, iamCfg, iamMetrics)
	if err != nil {
		return nil, err
	}
	if err := iamMetrics.RegisterBreakglassGauge(breakglassActiveCounter(roles, bindings)); err != nil {
		return nil, fmt.Errorf("iam breakglass gauge: %w", err)
	}
	// 约束检查器依赖指标做 shadow 期观测，因此必须在指标就绪后构造。
	constraintGuard, err := app.NewConstraintGuard(app.ConstraintGuardConfig{
		Mode:         constraintMode,
		Roles:        roles,
		Bindings:     bindings,
		SodGroups:    sodGroups,
		Restrictions: roleRestrictions,
		Observer:     iamMetrics,
		Logger:       deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam constraint guard: %w", err)
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

	// 多副本必须使用平台注入的节点号；未注入时失败关闭，禁止回退固定节点。
	ids := deps.IDs
	if ids == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	// 审计出口：默认直接落结构化日志（local / demo）；
	// async 启用后先写入通用 Outbox、再由 Relay 投递，进程崩溃不丢审计。
	auditEmitter := audit.SlogEmitter{Log: deps.Logger}
	recorder := observability.ChangeRecorder{
		Inner:   app.SlogRecorder{Emitter: auditEmitter},
		Metrics: iamMetrics,
	}
	if deps.Async != nil {
		store := deps.Async.Store()
		stats, ok := store.(outbox.TopicStatReader)
		if !ok {
			return nil, fmt.Errorf("iam async: outbox store does not support topic stats")
		}
		recorder = observability.ChangeRecorder{
			Inner:   app.OutboxRecorder{Events: store},
			Metrics: iamMetrics,
		}
		if gaugeErr := iamMetrics.RegisterOutboxGauges(topicStatsCounter(stats, app.TopicAudit)); gaugeErr != nil {
			return nil, fmt.Errorf("iam outbox gauges: %w", gaugeErr)
		}
		deps.Async.AddObserver(app.AuditTopicObserver{Metrics: iamMetrics})
		if registerErr := RegisterAsync(deps); registerErr != nil {
			return nil, registerErr
		}
	}
	auditMeta := app.AuditMeta{Service: cfg.Service, ActorType: auditActorType(cfg.Channel)}
	invalidator := iampcache.NewSubjectInvalidator(snapshotCache, cfg.Env)
	// 事务边界：MySQL 下把「业务写入 + 审计 outbox」合并为一次提交；
	// 内存仓储无法回滚，显式退化为无事务（见 app.NoopUnitOfWork）。
	uow := app.UnitOfWork(app.NoopUnitOfWork{})
	if deps.Database != nil {
		uow = persistence.NewSQLUnitOfWork(deps.Database.SQL())
	}

	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: invalidator,
		UnitOfWork:  uow,
		IDs:         ids,
		Audit:       auditMeta,
		Limits: app.RoleLimits{
			MaxPermissionsPerRole:     cfg.IAM.Limits.MaxPermissionsPerRole,
			MaxCustomRolesPerMerchant: cfg.IAM.Limits.MaxCustomRolesPerMerchant,
			RequireReason:             cfg.IAM.Security.RequireReason,
		},
		Constraints: constraintGuard,
		Logger:      deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam role service: %w", err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: invalidator,
		UnitOfWork:  uow,
		IDs:         ids,
		Audit:       auditMeta,
		Limits: app.BindingLimits{
			MaxBindingsPerSubject: cfg.IAM.Limits.MaxBindingsPerSubject,
			RequireReason:         cfg.IAM.Security.RequireReason,
		},
		Constraints: constraintGuard,
		Logger:      deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam binding service: %w", err)
	}
	sodGroupSvc, err := app.NewSodGroupService(app.SodGroupServiceConfig{
		Groups:     sodGroups,
		Roles:      roles,
		Violations: sodViolations,
		Recorder:   recorder,
		UnitOfWork: uow,
		IDs:        ids,
		Audit:      auditMeta,
		Limits:     app.SodGroupLimits{RequireReason: cfg.IAM.Security.RequireReason},
		Logger:     deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam sod group service: %w", err)
	}
	roleConstraintSvc, err := app.NewRoleConstraintService(app.RoleConstraintServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Constraints: roleRestrictions,
		SodGroups:   sodGroups,
		Recorder:    recorder,
		UnitOfWork:  uow,
		IDs:         ids,
		Audit:       auditMeta,
		Limits:      app.RoleConstraintLimits{RequireReason: cfg.IAM.Security.RequireReason},
		Logger:      deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam role constraint service: %w", err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, snapshots)
	if err != nil {
		return nil, fmt.Errorf("iam query service: %w", err)
	}
	menuRepo := buildMenuRepository(deps)
	menuSvc, err := app.NewMenuService(menuRepo, snapshots)
	if err != nil {
		return nil, fmt.Errorf("iam menu service: %w", err)
	}
	menuAdminSvc, err := app.NewMenuAdminService(app.MenuAdminConfig{
		Menus:      menuRepo,
		Recorder:   recorder,
		Components: menuview.Catalog{},
		UnitOfWork: uow,
		IDs:        ids,
		Audit:      auditMeta,
		Logger:     deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam menu admin service: %w", err)
	}
	templateSvc, err := app.NewTemplateService(app.TemplateServiceConfig{
		Templates:  templates,
		Roles:      roleSvc,
		RoleRepo:   roles,
		Recorder:   recorder,
		UnitOfWork: uow,
		IDs:        ids,
		Audit:      auditMeta,
		Limits: app.RoleLimits{
			MaxPermissionsPerRole:     cfg.IAM.Limits.MaxPermissionsPerRole,
			MaxCustomRolesPerMerchant: cfg.IAM.Limits.MaxCustomRolesPerMerchant,
			RequireReason:             cfg.IAM.Security.RequireReason,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam template service: %w", err)
	}

	applicator, err := app.NewChangeApplicator(roleSvc, bindingSvc, sodGroupSvc, roleConstraintSvc, templateSvc)
	if err != nil {
		return nil, fmt.Errorf("iam change applicator: %w", err)
	}
	changeSvc, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   buildChangeRequestRepository(deps),
		Applicator: applicator,
		UnitOfWork: uow,
		IDs:        ids,
		Limits: app.ChangeLimits{
			RequireReason: cfg.IAM.Security.RequireReason,
			// 000002 已补 merchant_id，B 端审批按 Principal 的商户隔离后开放。
			AllowBizChannel: true,
			AllowBreakglass: cfg.IAM.Breakglass.Enabled,
			// 审批单必须在意图过期前完成决策：迟到的批准等于授权了过期载荷。
			ApprovalTTL: defaultApprovalTTL,
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

	// 访问复核（P1-7）：默认开启；显式关闭时 Certification=nil，Handler 不注册复核路由。
	var certificationSvc *app.CertificationService
	if cfg.IAM.Certification.IsEnabled() {
		campaigns, tasks := buildCertificationRepositories(deps)
		certificationSvc, err = app.NewCertificationService(app.CertificationServiceConfig{
			Campaigns:  campaigns,
			Tasks:      tasks,
			Bindings:   bindings,
			Roles:      roles,
			Revoker:    bindingCertificationRevoker{svc: bindingSvc},
			Recorder:   recorder,
			UnitOfWork: uow,
			IDs:        ids,
			Audit:      auditMeta,
			Limits: app.CertificationLimits{
				RequireReason:   cfg.IAM.Security.RequireReason,
				AllowBizChannel: true,
				AutoRevoke:      cfg.IAM.Certification.AutoRevoke,
			},
			Logger: deps.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("iam certification service: %w", err)
		}
	}

	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles:           roleSvc,
		Bindings:        bindingSvc,
		Query:           querySvc,
		Changes:         changeSvc,
		Breakglass:      breakglassSvc,
		Templates:       templateSvc,
		Menus:           menuSvc,
		MenuAdmin:       menuAdminSvc,
		SodGroups:       sodGroupSvc,
		RoleConstraints: roleConstraintSvc,
		Certification:   certificationSvc,
		Channel:         domain.Channel(cfg.Channel),
		RequireApproval: cfg.IAM.Approval.Enabled,
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

// BuildRepositories 按 db.driver 选择内存或 MySQL 仓储。
//
// 导出原因：tenantruntime 的角色绑定同步（T-18，ADR-0007）
// 需要同一套 IAM 仓储（同库同事务），不能各建各的。
func BuildRepositories(deps *platform.Deps) (
	domain.RoleRepository,
	domain.BindingRepository,
	domain.RoleTemplateRepository,
	domain.SnapshotRepository,
	error,
) {
	if deps.Database == nil {
		roles := persistence.NewMemoryRoleRepository()
		bindings := persistence.NewMemoryBindingRepository()
		templates := persistence.NewMemoryRoleTemplateRepository()
		return roles, bindings, templates, persistence.NewMemorySnapshotRepository(roles, bindings), nil
	}
	sqlDB := deps.Database.SQL()
	return persistence.NewSQLRoleRepository(sqlDB),
		persistence.NewSQLBindingRepository(sqlDB),
		persistence.NewSQLRoleTemplateRepository(sqlDB),
		persistence.NewSQLSnapshotRepository(sqlDB),
		nil
}

// bindingCertificationRevoker 把访问复核的回收动作适配到 BindingService.Unbind。
//
// 复用既有解绑用例，确保软撤销、约束校验、快照失效与 rbac.binding_change 审计
// 都走同一条链路；复核侧只额外记录 rbac.certification 审计。
type bindingCertificationRevoker struct {
	svc *app.BindingService
}

// Revoke 回收绑定；MerchantID 留空，因为调用前已校验绑定与活动商户一致。
func (r bindingCertificationRevoker) Revoke(ctx context.Context, bindingID, actor, reason string) error {
	return r.svc.Unbind(ctx, app.UnbindCommand{ID: bindingID, Actor: actor, Reason: reason})
}

// buildCertificationRepositories 装配访问复核仓储；内存与 SQL 语义保持一致。
func buildCertificationRepositories(deps *platform.Deps) (
	domain.CertificationCampaignRepository, domain.CertificationTaskRepository,
) {
	if deps.Database == nil {
		return persistence.NewMemoryCertificationCampaignRepository(),
			persistence.NewMemoryCertificationTaskRepository()
	}
	sqlDB := deps.Database.SQL()
	return persistence.NewSQLCertificationCampaignRepository(sqlDB),
		persistence.NewSQLCertificationTaskRepository(sqlDB)
}

// buildConstraintRepositories 装配 RBAC2 约束仓储（互斥组 / 前置角色）。
//
// 内存与 SQL 必须与角色仓储保持一致：否则影子期会把冲突记录在另一份数据上，
// 造成「验证通过、生产不通过」的假象。
func buildConstraintRepositories(deps *platform.Deps) (domain.SodGroupRepository, domain.RoleConstraintRepository) {
	if deps.Database == nil {
		return persistence.NewMemorySodGroupRepository(), persistence.NewMemoryRoleConstraintRepository()
	}
	sqlDB := deps.Database.SQL()
	return persistence.NewSQLSodGroupRepository(sqlDB), persistence.NewSQLRoleConstraintRepository(sqlDB)
}

// buildSodViolationRepository 装配互斥组存量预检仓储。
//
// 内存实现复用同一份角色 / 绑定仓储，保证与 Guard、快照看到相同的数据。
func buildSodViolationRepository(
	deps *platform.Deps, roles domain.RoleRepository, bindings domain.BindingRepository,
) domain.SodViolationRepository {
	if deps.Database == nil {
		return persistence.NewMemorySodViolationRepository(roles, bindings)
	}
	return persistence.NewSQLSodViolationRepository(deps.Database.SQL())
}

// RegisterAsync 注册 IAM 异步 topic handler（API 与 worker 共用）。
//
// 审计事件由本 handler 投递到审计出口；历史 cache.invalidate 事件按无操作消费，
// 避免迁移后残留事件反复进入死信。
func RegisterAsync(deps *platform.Deps) error {
	if deps == nil || deps.Async == nil {
		return nil
	}
	handler := app.AuditEventHandler(audit.SlogEmitter{Log: deps.Logger})
	for _, topic := range []string{app.TopicAudit, app.TopicCache} {
		if err := deps.Async.Register(topic, handler); err != nil {
			return fmt.Errorf("iam async handler %s: %w", topic, err)
		}
	}
	return nil
}

// topicStatsCounter 采集 IAM 主题积压数与最老事件延迟，供 GaugeFunc 实时查询。
func topicStatsCounter(reader outbox.TopicStatReader, topic string) func() (int, float64, error) {
	return func() (int, float64, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stats, err := reader.TopicStats(ctx, topic)
		if err != nil {
			return 0, 0, err
		}
		lag := 0.0
		if !stats.OldestCreatedAt.IsZero() {
			lag = time.Since(stats.OldestCreatedAt).Seconds()
			if lag < 0 {
				lag = 0
			}
		}
		return stats.Pending, lag, nil
	}
}

// buildMenuRepository 按 db.driver 选择菜单仓储。
//
// 菜单由 SQL 种子维护；内存模式（local / demo / 单测）没有种子来源，返回空仓储，
// 前端切换 accessMode=backend 时应接入 MySQL，否则菜单为空。
func buildMenuRepository(deps *platform.Deps) domain.MenuRepository {
	if deps.Database == nil {
		return persistence.NewMemoryMenuRepository()
	}
	return persistence.NewSQLMenuRepository(deps.Database.SQL())
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
//
// observer 为 nil 时退化为 NopObserver（缓存指标不可观测）；生产必须传入指标观测器。
func buildSnapshotCache(deps *platform.Deps, iamCfg iamconfig.IAMConfig, observer iampcache.Observer) (*iampcache.SnapshotCache, error) {
	cfg := iamCfg.Cache
	var l2 pkgcache.Store
	if deps.Redis != nil {
		l2 = iampcache.NewRedisStore(deps.Redis.Cache())
	}
	if cfg.Driver == iamconfig.IAMCacheDriverRedis && l2 == nil {
		return nil, apperr.Internal("iam cache driver=redis but redis is not configured", nil)
	}
	layers := iampcache.Config{
		L1:          pkgcache.NewMemory(pkgcache.MemoryConfig{MaxEntries: cfg.L1MaxEntries}),
		L2:          l2,
		L1TTL:       cfg.L1TTL,
		L2TTL:       cfg.L2TTL,
		NegativeTTL: cfg.NegativeTTL,
		// 必须注入观测器：否则快照缓存退化为 NopObserver，
		// iam_authz_cache_total 恒为空，看板「快照缓存命中」面板永远没有数据
		// （ENT-06 T6 实测）。
		Observer: observer,
	}
	if cfg.Driver == iamconfig.IAMCacheDriverMemory {
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

package iamruntime

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	iampcache "github.com/admin-frame/admin-frame/internal/iam/infrastructure/cache"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// BuildCertificationSweeper 装配一次性超期扫描所需的访问复核用例。
//
// worker 进程的 channel=worker 时 New 会直接返回（不提供 IAM HTTP handler），
// 因此这里复用与 A 端完全相同的仓储、解绑用例、limits 与审计出口，只省略
// 与扫描无关的角色模板 / 菜单 / 变更单等用例。返回 nil 表示访问复核已显式关闭。
//
// 复用同一套装配的原因：扫描会执行 auto_revoke，必须与人工解绑走同一条
// 软撤销 + 约束校验 + 快照失效链路；另写一条回收路径等于绕开这些校验。
func BuildCertificationSweeper(
	deps *platform.Deps, iamCfg iamconfig.IAMConfig,
) (*app.CertificationService, error) {
	if deps == nil || deps.Config == nil {
		return nil, apperr.Internal("iam certification sweeper requires platform deps", nil)
	}
	if !iamCfg.Certification.IsEnabled() {
		return nil, nil
	}
	// 复核任务、审计事件与解绑记录都需要主键；缺失时失败关闭而不是生成随机 ID。
	if deps.IDs == nil {
		return nil, apperr.Internal("iam id generator is required", nil)
	}
	roles, bindings, _, _, err := BuildRepositories(deps)
	if err != nil {
		return nil, fmt.Errorf("iam repositories: %w", err)
	}
	uow := app.UnitOfWork(app.NoopUnitOfWork{})
	if deps.Database != nil {
		uow = persistence.NewSQLUnitOfWork(deps.Database.SQL())
	}
	// 审计出口与 API 进程一致：async 启用时写通用 Outbox，由常驻 worker 投递；
	// 未启用时只写结构化日志（本地 / demo），与 New 的降级语义相同。
	recorder := app.ChangeRecorder(app.SlogRecorder{
		Emitter: platformaudit.SlogEmitter{Log: deps.Logger},
	})
	if deps.Async != nil {
		recorder = app.OutboxRecorder{Events: deps.Async.Store()}
	}
	// 解绑必须让共享快照缓存失效，否则被回收的权限在 L2 TTL 内仍然生效。
	// 一次性进程不做缓存读，观测器传 nil（缓存包会退化为 NopObserver）。
	snapshotCache, err := buildSnapshotCache(deps, iamCfg, nil)
	if err != nil {
		return nil, fmt.Errorf("iam snapshot cache: %w", err)
	}
	auditMeta := app.AuditMeta{Service: deps.Config.Service, ActorType: "system"}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		Invalidator: iampcache.NewSubjectInvalidator(snapshotCache, deps.Config.Env),
		UnitOfWork:  uow,
		IDs:         deps.IDs,
		Audit:       auditMeta,
		Limits: app.BindingLimits{
			MaxBindingsPerSubject: iamCfg.Limits.MaxBindingsPerSubject,
			RequireReason:         iamCfg.Security.RequireReason,
		},
		Logger: deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("iam binding service: %w", err)
	}
	svc, err := buildCertificationService(deps, iamCfg, certificationServiceDeps{
		Roles:      roles,
		Bindings:   bindings,
		BindingSvc: bindingSvc,
		Recorder:   recorder,
		UnitOfWork: uow,
		IDs:        deps.IDs,
		Audit:      auditMeta,
	})
	if err != nil {
		return nil, fmt.Errorf("iam certification service: %w", err)
	}
	return svc, nil
}

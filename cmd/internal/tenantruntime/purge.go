package tenantruntime

// ENT-15 数据处置任务装配：PurgeService 与周期扫描（D-05 第三段执行）。

import (
	"context"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	identitypersistence "github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	tenantapp "github.com/admin-frame/admin-frame/internal/tenant/app"
	tenantconfig "github.com/admin-frame/admin-frame/internal/tenant/config"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	tenantpersistence "github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// manifestRepo 统一内存 / MySQL manifest 仓储的方法集（domain.ExportManifestRepository）。
type manifestRepo = domain.ExportManifestRepository

// buildPurgeService 构造处置服务并注册周期任务。
//
// 边界清单（tenant-data-lifecycle.md §各域数据与硬删除边界）：租户四表 →
// IAM 商户数据 → identity 商户数据；审计事件流不在任何边界（处置证据）。
// 数据库缺失（纯内存最小形态）只挂租户内存边界；导出证据缺失的租户一律
// 跳过并告警——绝不裸删。
func buildPurgeService(deps *platform.Deps, tenants domain.Repository, tenantCfg tenantconfig.TenantConfig) (*tenantapp.PurgeService, error) {
	if deps == nil || deps.Config == nil || tenants == nil {
		return nil, nil
	}

	var manifests manifestRepo
	var boundaries []tenantapp.Boundary

	if deps.Database != nil {
		sqlDB := deps.Database.SQL()
		mysqlManifest, err := tenantpersistence.NewMySQLExportManifestRepository(sqlDB)
		if err != nil {
			return nil, fmt.Errorf("tenant export manifest repository: %w", err)
		}
		manifests = mysqlManifest
		tenantPurger, err := tenantpersistence.NewMySQLTenantPurger(sqlDB)
		if err != nil {
			return nil, fmt.Errorf("tenant purger: %w", err)
		}
		boundaries = append(boundaries, tenantPurger)
		// IAM / identity 边界（D-05 级联任务；各自域的 persistence 提供）。
		iamPurger, err := persistence.NewMySQLMerchantPurger(sqlDB)
		if err != nil {
			return nil, fmt.Errorf("iam purger: %w", err)
		}
		boundaries = append(boundaries, iamPurger)
		identityPurger, err := identitypersistence.NewMySQLMerchantPurger(sqlDB)
		if err != nil {
			return nil, fmt.Errorf("identity purger: %w", err)
		}
		boundaries = append(boundaries, identityPurger)
	} else {
		manifests = tenantpersistence.NewMemoryExportManifestRepository()
		memoryRepo, ok := tenants.(*tenantpersistence.MemoryRepository)
		if !ok {
			return nil, fmt.Errorf("tenant purge requires memory repository in memory mode")
		}
		boundaries = append(boundaries, tenantpersistence.NewMemoryTenantPurger(memoryRepo))
	}

	evidence, err := tenantapp.NewManifestEvidence(manifests)
	if err != nil {
		return nil, err
	}
	opts := tenantapp.PurgeOptions{
		Service:    deps.Config.Service,
		HoldDays:   tenantCfg.Retention.HoldDays,
		ExportDays: tenantCfg.Retention.ExportDays,
		BatchSize:  tenantCfg.Retention.PurgeBatchSize,
	}
	svc, err := tenantapp.NewPurgeService(tenants, manifests, evidence, boundaries, purgeEmitter(deps), nil, opts)
	if err != nil {
		return nil, fmt.Errorf("tenant purge service: %w", err)
	}
	registerPurgeLoop(deps, svc)
	return svc, nil
}

// purgeEmitter 处置审计出口：与租户域写用例同源（Outbox 优先，与业务
// 事务一致性由 Relay 保证）；无 DB 时退回结构化日志（内存模式无处置对象）。
func purgeEmitter(deps *platform.Deps) audit.Emitter {
	if deps.Async != nil && deps.Async.Store() != nil {
		return platformaudit.OutboxEmitter{Topic: tenantapp.TopicAudit, Events: deps.Async.Store()}
	}
	if deps.Database != nil {
		return platformaudit.MultiEmitter{
			platformaudit.SQLEmitter{DB: deps.Database.SQL()},
			platformaudit.SlogEmitter{Log: deps.Logger},
		}
	}
	return platformaudit.SlogEmitter{Log: deps.Logger}
}

// registerPurgeLoop 注册周期扫描（默认 1 小时一轮；任务幂等——
// 删除按边界推进，重跑只删剩余行）。
func registerPurgeLoop(deps *platform.Deps, svc *tenantapp.PurgeService) {
	if deps == nil || deps.Background == nil || svc == nil {
		return
	}
	deps.Background(func(ctx context.Context) error {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				report, err := svc.RunDue(ctx)
				if err != nil {
					// 任务失败只记录不退出（下一轮重试；连续失败由告警跟进）。
					deps.Logger.Error("tenant purge task failed", "error", err)
					continue
				}
				if report.Scanned > 0 {
					deps.Logger.Info("tenant purge task done",
						"scanned", report.Scanned, "purged", report.Purged,
						"skipped", report.Skipped, "rows", report.RowsDeleted)
				}
			}
		}
	})
}

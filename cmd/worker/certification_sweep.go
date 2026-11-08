// Package main 是后台任务进程入口（Outbox Relay、保留期清理、一次性补偿任务）。
package main

import (
	"context"
	"log/slog"

	iamruntime "github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// runCertificationSweep 执行一轮访问复核超期扫描后退出。
//
// 入口形态与 -replay-dlq 一致：Kubernetes CronJob 起一次性 Pod，不常驻。
// 之所以不做进程内定时器：复核回收要复用解绑用例与快照失效，这些依赖只在
// 完整装配的进程里存在；一次性进程既避免了 API 多副本重复执行，也让单次
// 执行结果直接体现在 Job 的成功 / 失败与日志里（RBAC 设计见 rbac-iam.md §10.3）。
func runCertificationSweep(
	ctx context.Context, deps *platform.Deps, iamCfg iamconfig.IAMConfig, logger *slog.Logger,
) error {
	// 共享快照缓存在 Redis 上；扫描进程必须能删缓存键，否则回收要等 TTL 生效。
	// 未配置 redis 时 OpenRedis 返回 nil，解绑退化为只影响本进程内存缓存。
	if deps.Redis == nil {
		client, err := platform.OpenRedis(ctx, deps.Config.Redis)
		if err != nil {
			return err
		}
		if client != nil {
			deps.Redis = client
			defer func() {
				if closeErr := client.Close(); closeErr != nil {
					logger.Warn("redis close", "err", closeErr)
				}
			}()
		}
	}
	svc, err := iamruntime.BuildCertificationSweeper(deps, iamCfg)
	if err != nil {
		return err
	}
	if svc == nil {
		logger.Info("certification sweep skipped", "reason", "iam.certification.enabled=false")
		return nil
	}
	result, sweepErr := svc.SweepOverdue(ctx)
	logger.Info("certification sweep finished",
		"scanned", result.Scanned,
		"overdue", result.Overdue,
		"expired", result.Expired,
		"failed", result.Failed,
		"tasks", result.Tasks,
		"auto_revoke", iamCfg.Certification.AutoRevoke,
	)
	return sweepErr
}

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// certificationSweepPageSize 是扫描进行中活动时的分页大小。
//
// 与 API 列表上限（100）对齐：复用同一仓储语义，避免扫描引入额外的深分页 SQL。
const certificationSweepPageSize = 100

// certificationSweepMaxPages 是单轮扫描的页数上限，防止异常数据导致死循环。
//
// 100 页 × 100 条 = 10000 个进行中活动；超过该规模的复核活动本身即说明
// 生命周期管理失当，扫描应显式失败而不是静默拖长事务窗口。
const certificationSweepMaxPages = 100

// CertificationSweepResult 汇总一轮超期扫描的结果。
//
// 字段全部为计数，便于直接打日志 / 作为 CronJob 退出信息；
// 扫描是一次性进程（worker -certification-sweep），不注册 Prometheus 指标。
type CertificationSweepResult struct {
	// Scanned 本轮扫描的进行中活动数。
	Scanned int
	// Overdue 其中已过截止时间的活动数。
	Overdue int
	// Expired 成功完成超期处理（标记 expired / 自动回收）的活动数。
	Expired int
	// Failed 处理失败的活动数；失败不阻断其余活动。
	Failed int
	// Tasks 受影响的任务数（含 expired / revoked / skipped）。
	Tasks int
}

// SweepOverdue 扫描全部进行中活动并处理已过期活动的待裁决任务。
//
// 供外部调度使用（Helm CronJob → worker -certification-sweep）：本方法不接收
// 作用域，因为系统任务不存在「哪个端在看」的概念；可见性由调用方在装配期
// 保证（只有 cmd 层能构造出这个用例）。人工触发仍走 AutoExpireOverdue，
// 两条路径共用 expireOverdueTasks，避免出现第二套状态机。
//
// 每个活动独立事务：单个活动失败（例如审计写入失败）只影响该活动，
// 其余活动继续处理，最终把失败聚合成一个 error 返回给调度方。
func (s *CertificationService) SweepOverdue(ctx context.Context) (CertificationSweepResult, error) {
	var result CertificationSweepResult
	now := time.Now().UTC()
	ids, scanned, err := s.overdueCampaignIDs(ctx, now)
	result.Scanned = scanned
	if err != nil {
		return result, err
	}
	result.Overdue = len(ids)
	if len(ids) == 0 {
		return result, nil
	}
	failures := make([]error, 0, len(ids))
	for _, id := range ids {
		affected, expireErr := s.expireCampaign(ctx, id, now)
		if expireErr != nil {
			// 扫描期间活动被人工关闭属正常竞争，不计失败也不重试。
			if errors.Is(expireErr, apperr.ErrConflict) {
				s.log.Debug("certification sweep skipped closed campaign", "campaign_id", id)
				continue
			}
			result.Failed++
			failures = append(failures, fmt.Errorf("campaign %s: %w", id, expireErr))
			s.log.Warn("certification sweep campaign failed", "campaign_id", id, "error", expireErr)
			continue
		}
		result.Expired++
		result.Tasks += affected
	}
	return result, errors.Join(failures...)
}

// overdueCampaignIDs 分页列出进行中活动，返回其中已过期的活动 ID。
//
// 先只读收集再逐个处理：处理会改变任务状态但不会改变活动状态，
// 即便如此也避免「边扫描边写入」导致的分页漂移。
func (s *CertificationService) overdueCampaignIDs(
	ctx context.Context, now time.Time,
) (ids []string, scanned int, err error) {
	for page := 0; page < certificationSweepMaxPages; page++ {
		campaigns, listErr := s.campaigns.List(ctx, domain.CertificationCampaignFilter{
			Status: domain.CertificationStatusActive,
			Limit:  certificationSweepPageSize,
			Offset: page * certificationSweepPageSize,
		})
		if listErr != nil {
			return nil, scanned, listErr
		}
		scanned += len(campaigns)
		for _, campaign := range campaigns {
			if campaign.IsOverdue(now) {
				ids = append(ids, campaign.ID)
			}
		}
		if len(campaigns) < certificationSweepPageSize {
			return ids, scanned, nil
		}
	}
	return nil, scanned, apperr.Internal("certification sweep exceeded page limit", nil)
}

// expireCampaign 在独立事务内处理单个活动的超期任务。
func (s *CertificationService) expireCampaign(
	ctx context.Context, campaignID string, now time.Time,
) (int, error) {
	affected := 0
	err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		campaign, err := s.campaigns.FindByID(ctx, campaignID)
		if err != nil {
			return err
		}
		affected, err = s.expireOverdueTasks(ctx, campaign, now)
		return err
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

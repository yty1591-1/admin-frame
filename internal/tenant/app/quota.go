package app

import (
	"context"
	"strconv"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// ActionQuotaChange 配额变更审计动作。
const ActionQuotaChange = "tenant.quota_change"

// QuotaService 编排租户配额用例。
//
// 只做「上限配置」，不做用量计数与拒绝：执行属 ENT-14（限流与配额），
// 本服务与执行层通过 `tenant_quota` 表解耦，避免两个域互相调用。
type QuotaService struct {
	tenants domain.Repository
	quotas  domain.QuotaRepository
	audit   auditContext
	now     func() time.Time
}

// NewQuotaService 构造配额用例；依赖必填。
func NewQuotaService(tenants domain.Repository, quotas domain.QuotaRepository, emitter audit.Emitter, opts Options) (*QuotaService, error) {
	if tenants == nil {
		return nil, apperr.Internal("tenant quota service requires a tenant repository", nil)
	}
	if quotas == nil {
		return nil, apperr.Internal("tenant quota service requires a quota repository", nil)
	}
	if emitter == nil {
		return nil, apperr.Internal("tenant quota service requires an audit emitter", nil)
	}
	now := time.Now
	return &QuotaService{
		tenants: tenants,
		quotas:  quotas,
		audit:   auditContext{emitter: emitter, service: opts.Service, now: now},
		now:     now,
	}, nil
}

// SetQuotaInput 是设置配额入参。
type SetQuotaInput struct {
	// TenantID 租户 ID。
	TenantID string
	// Resource 资源键，如 user / api.rps。
	Resource string
	// Limit 上限；-1 表示不限。
	Limit int64
	// Actor 操作者。
	Actor Actor
}

// Set 设置（或覆盖）某资源的配额上限。
func (s *QuotaService) Set(ctx context.Context, in SetQuotaInput) (domain.Quota, error) {
	if _, err := mutableTenant(ctx, s.tenants, in.TenantID); err != nil {
		return domain.Quota{}, err
	}
	quota, err := domain.NewQuota(in.TenantID, in.Resource, in.Limit, s.now())
	if err != nil {
		return domain.Quota{}, translate(err)
	}
	if err := s.quotas.UpsertQuota(ctx, quota); err != nil {
		return domain.Quota{}, translate(err)
	}
	s.audit.emit(ctx, in.Actor, ActionQuotaChange, ResourceTypeQuota, quota.TenantID,
		audit.OutcomeSuccess, "", map[string]string{
			"resource": quota.Resource,
			"limit":    quotaLimitString(quota.Limit),
		})
	return quota, nil
}

// List 列出租户已配置的配额；未配置的资源不返回。
func (s *QuotaService) List(ctx context.Context, tenantID string) ([]domain.Quota, error) {
	if _, err := mutableTenant(ctx, s.tenants, tenantID); err != nil {
		return nil, err
	}
	quotas, err := s.quotas.ListQuotas(ctx, tenantID)
	if err != nil {
		return nil, translate(err)
	}
	return quotas, nil
}

// quotaLimitString 输出配额上限：-1 显示为 unlimited，便于审计直接读懂。
func quotaLimitString(limit int64) string {
	if limit == domain.QuotaUnlimited {
		return "unlimited"
	}
	return strconv.FormatInt(limit, 10)
}

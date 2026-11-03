package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MaxPlanCodeLength 套餐编码长度上限。
const MaxPlanCodeLength = 64

// Plan 是套餐模板：配额默认值的目录。
//
// 语义是「模板」而不是「运行时绑定」：租户创建时把套餐配额物化进
// tenant_quota，之后套餐调整只影响新建租户，存量租户保持开账时的上限——
// 与合同结算语义一致（账面不变，除非运营显式改配额）。
type Plan struct {
	// ID 套餐 ID。
	ID string
	// Code 人类可读编码（如 standard / enterprise），全局唯一。
	Code string
	// Name 展示名。
	Name string
	// IsDefault 创建租户未指定套餐时使用的默认套餐；目录内应恰好一个。
	IsDefault bool
	// Quotas 套餐默认配额；资源键与 tenant_quota.resource 同一约束。
	Quotas []Quota
	// CreatedAt / UpdatedAt 时间戳。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate 校验套餐自洽性。
func (p Plan) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("tenant: plan id is required")
	}
	code := strings.TrimSpace(p.Code)
	if code == "" {
		return ErrPlanCodeRequired
	}
	if len([]rune(code)) > MaxPlanCodeLength {
		return ErrPlanCodeTooLong
	}
	if !validResourceKey(code) {
		return &InvalidResourceError{Resource: code}
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("tenant: plan name is required")
	}
	// 套餐配额是模板：没有 TenantID，不能复用 Quota.Validate（它要求租户维度）。
	// 这里只校验资源键与上限取值，与 tenant_quota 落库时的约束保持一致。
	seen := make(map[string]struct{}, len(p.Quotas))
	for _, quota := range p.Quotas {
		if _, err := domainValidateQuotaTemplate(quota); err != nil {
			return err
		}
		if _, dup := seen[quota.Resource]; dup {
			return fmt.Errorf("tenant: plan %s has duplicate quota resource %s", p.Code, quota.Resource)
		}
		seen[quota.Resource] = struct{}{}
	}
	return nil
}

// domainValidateQuotaTemplate 校验模板配额的资源键与上限（不要求 TenantID）。
func domainValidateQuotaTemplate(quota Quota) (struct{}, error) {
	resource := strings.TrimSpace(quota.Resource)
	if resource == "" {
		return struct{}{}, ErrResourceRequired
	}
	if !validResourceKey(resource) {
		return struct{}{}, &InvalidResourceError{Resource: quota.Resource}
	}
	if quota.Limit < QuotaUnlimited {
		return struct{}{}, fmt.Errorf("tenant: quota limit must be >= %d, got %d", QuotaUnlimited, quota.Limit)
	}
	return struct{}{}, nil
}

// SortedQuotas 返回按资源键排序的配额副本，保证展示与落库稳定。
func (p Plan) SortedQuotas() []Quota {
	out := make([]Quota, len(p.Quotas))
	copy(out, p.Quotas)
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

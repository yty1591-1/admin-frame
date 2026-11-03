package domain

import (
	"fmt"
	"strings"
	"time"
)

// QuotaUnlimited 表示不限量的配额取值。
const QuotaUnlimited int64 = -1

// MaxResourceLength 配额资源键长度上限（与 tenant_quota.resource 对齐）。
const MaxResourceLength = 64

// Quota 是租户在某个资源上的上限。
//
// 本表只定义「上限是多少」，执行（计数、拒绝、42900）由 ENT-14 负责；
// 领域层不做用量统计，避免把限流状态混进租户主数据。
type Quota struct {
	// TenantID 所属租户。
	TenantID string
	// Resource 资源键，如 user / api.rps / storage.mb。
	Resource string
	// Limit 上限；QuotaUnlimited(-1) 表示不限。
	Limit int64
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// NewQuota 构造配额；limit 允许 -1（不限），不允许其它负值。
func NewQuota(tenantID, resource string, limit int64, now time.Time) (Quota, error) {
	trimmedTenant := strings.TrimSpace(tenantID)
	if trimmedTenant == "" {
		return Quota{}, fmt.Errorf("tenant: quota tenant id is required")
	}
	trimmedResource := strings.TrimSpace(resource)
	if trimmedResource == "" {
		return Quota{}, ErrResourceRequired
	}
	if len([]rune(trimmedResource)) > MaxResourceLength {
		return Quota{}, ErrResourceTooLong
	}
	if !validResourceKey(trimmedResource) {
		return Quota{}, &InvalidResourceError{Resource: trimmedResource}
	}
	if limit < QuotaUnlimited {
		return Quota{}, fmt.Errorf("tenant: quota limit must be >= %d, got %d", QuotaUnlimited, limit)
	}
	return Quota{
		TenantID:  trimmedTenant,
		Resource:  trimmedResource,
		Limit:     limit,
		UpdatedAt: now.UTC(),
	}, nil
}

// Validate 校验配额实体自洽性。
func (q Quota) Validate() error {
	if strings.TrimSpace(q.TenantID) == "" {
		return fmt.Errorf("tenant: quota tenant id is required")
	}
	if strings.TrimSpace(q.Resource) == "" {
		return ErrResourceRequired
	}
	if !validResourceKey(q.Resource) {
		return &InvalidResourceError{Resource: q.Resource}
	}
	if q.Limit < QuotaUnlimited {
		return fmt.Errorf("tenant: quota limit must be >= %d, got %d", QuotaUnlimited, q.Limit)
	}
	return nil
}

// Unlimited 判断是否不限量。
func (q Quota) Unlimited() bool {
	return q.Limit == QuotaUnlimited
}

// validResourceKey 只放行小写字母、数字与 `.` `_` `-`。
//
// 资源键会出现在配置、指标标签与限流键里；限定字符集可以避免它变成
// 注入点或指标基数炸弹（例如带空格 / 星号的键）。
func validResourceKey(resource string) bool {
	if resource == "" {
		return false
	}
	for _, r := range resource {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

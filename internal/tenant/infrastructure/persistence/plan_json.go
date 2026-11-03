package persistence

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// planQuotaJSON 与 tenant_plan.quotas 的 JSON 结构对齐：
// [{"resource":"user","limit":50}]。数组而不是对象，便于 JSON_TABLE 直接展开。
type planQuotaJSON struct {
	Resource string `json:"resource"`
	Limit    int64  `json:"limit"`
}

// decodePlanQuotas 解析套餐配额 JSON 并还原为领域实体。
//
// 空 JSON 视为「无默认配额」（合法：套餐可以只改名字不改配额）；
// 资源键非法或 limit 缺失视为目录数据损坏，返回错误——
// 错误的默认值会在创建租户时静默生效，宁可启动期报错。
func decodePlanQuotas(raw []byte) ([]domain.Quota, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var items []planQuotaJSON
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("tenant: decode plan quotas: %w", err)
	}
	// 模板配额没有租户维度，不走 NewQuota（它要求 TenantID）；资源键与上限
	// 在 Plan.Validate / encodePlanQuotas 写入侧已校验，此处只做形状还原。
	out := make([]domain.Quota, 0, len(items))
	for _, item := range items {
		if item.Resource == "" || item.Limit < domain.QuotaUnlimited {
			return nil, fmt.Errorf("tenant: plan quota %q/%d is malformed", item.Resource, item.Limit)
		}
		out = append(out, domain.Quota{Resource: item.Resource, Limit: item.Limit, UpdatedAt: timeNowUTC()})
	}
	return out, nil
}

// timeNowUTC 是包内统一时钟：解码场景不需要注入时钟。
func timeNowUTC() time.Time { return time.Now().UTC() }

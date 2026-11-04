package featureflagruntime

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/featureflag/app"
	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
)

// TenantEvaluator 是租户域消费端口的导出适配器（业务域不 import
// featureflag 内部，规划 §2.H）。
type TenantEvaluator struct {
	inner *app.EvaluationService
}

// NewTenantEvaluator 构造租户域消费端口适配器。
func NewTenantEvaluator(inner *app.EvaluationService) *TenantEvaluator {
	return &TenantEvaluator{inner: inner}
}

// IsEnabled 实现 tenantapp.FeatureEvaluator；ctx 用后台背景上下文
// （评估路径无请求事务，缓存命中时零 I/O）。
func (e *TenantEvaluator) IsEnabled(flagKey, channel, tenantID, subjectID string) bool {
	return e.inner.IsEnabled(context.Background(), flagKey, domain.EvalContext{
		Channel: channel, TenantID: tenantID, SubjectID: subjectID,
	})
}

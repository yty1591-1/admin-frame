// Package runtimebind 是 cmd 装配层在跨域运行时间传递的绑定集合。
//
// 平台 Deps 只承载平台能力；域之间的协作判定（如租户状态、成员关系）
// 由 cmd 显式传递，避免 internal/platform 持有业务语义函数字段
// （LAYER-09 / ARCHITECTURE §5.6「不承载业务语义」）。
package runtimebind

import "context"

// Tenant 是租户域向其他域提供的装配期判定绑定。
//
// 字段为 nil 表示本端未启用对应能力：调用方必须按失败关闭处理，
// 不得把「未注入」当作允许。
type Tenant struct {
	// VerifyTenant 判定商户当前是否可服务（ENT-09）；用于登录 / 开通前校验。
	VerifyTenant func(ctx context.Context, merchantID string) (bool, error)
	// IsTenantMember 判定主体是否为租户在册成员（ENT-09 T-22）。
	IsTenantMember func(ctx context.Context, tenantID, subjectID string) (bool, error)
}

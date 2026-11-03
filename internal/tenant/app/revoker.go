package app

import (
	"context"
)

// SessionRevoker 撤销一个租户名下全部会话与令牌（ENT-09 D-02=① 立即撤销）。
//
// 端口由 identity 实现（identity/app.TenantSessionRevoker）：租户域不 import
// 身份域，两个域经 cmd 装配层对接。撤销失败必须让停用 / 终止失败——
// 不允许出现「状态已停用、令牌还能换新」。
type SessionRevoker interface {
	// RevokeByTenant 撤销租户（merchant_id = tenantID）名下全部会话，返回数量。
	RevokeByTenant(ctx context.Context, tenantID string) (int, error)
}

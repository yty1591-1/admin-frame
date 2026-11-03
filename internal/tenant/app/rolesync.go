package app

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// RoleSyncer 是租户成员角色 → IAM 绑定的同步端口（T-18，ADR-0007）。
//
// 接口反转：租户域不 import IAM；实现在 cmd 装配层组合 IAM 的角色与绑定
// 仓储（rolesyncAdapter）。语义是「幂等收敛」——每次成员变更后调用一次，
// 实现方把该主体的模板角色绑定调整到与目标角色一致（多退少补）。
//
// 事务契约：实现必须复用调用方事务（ctx 中的 UoW），同步失败返回 error
// 让成员变更整体回滚（fail closed）——「成员已加、绑定失败」比「加成员失败」
// 更危险：运营以为交付完成，用户却毫无权限，故障要到投诉才暴露。
type RoleSyncer interface {
	// EnsureTemplates 物化租户的三个模板角色（tenant_owner / tenant_admin /
	// tenant_member），幂等；由 Create 在事务内调用（与配额物化同批）。
	EnsureTemplates(ctx context.Context, tenantID string) error

	// SyncRole 使主体在该租户内的 IAM 绑定与目标成员角色一致：
	// role 非 nil → 绑定对应模板角色（已撤销/过期的绑定刷新生效）；
	// role 为 nil → 解除该主体的模板角色绑定（成员停用）。
	//
	// 只增删模板角色的绑定，不碰商户自建角色（custom）的绑定。
	SyncRole(ctx context.Context, tenantID, subjectID string, role *domain.MemberRole) error
}

// noopRoleSyncer 是 RoleSyncer 的空实现；未装配 IAM 的形态（内存模式 / 单测）使用。
type noopRoleSyncer struct{}

// EnsureTemplates 空实现。
func (noopRoleSyncer) EnsureTemplates(context.Context, string) error { return nil }

// SyncRole 空实现。
func (noopRoleSyncer) SyncRole(context.Context, string, string, *domain.MemberRole) error { return nil }

// roleSyncerOrNoop 把 nil 同步端口替换为空实现：调用方无需判空。
func roleSyncerOrNoop(s RoleSyncer) RoleSyncer {
	if s == nil {
		return noopRoleSyncer{}
	}
	return s
}

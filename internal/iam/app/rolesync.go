package app

// 成员角色 → IAM 绑定同步（T-18，ADR-0007）。
//
// 组合 IAM 的角色与绑定仓储，把成员角色收敛为模板角色绑定（多退少补）。
// 事务契约：所有写入经 ctx 内的 UoW（两域共用同一 *sql.DB 时自动复用
// 租户域开启的事务），任一步失败返回 error 让成员变更整体回滚。

import (
	"context"
	"errors"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	tenantdomain "github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// roleSyncActor 是同步动作在 IAM 绑定里的操作者标识（granted_by / revoked_by）。
const roleSyncActor = "tenant-role-sync"

// templateCodePrefix 模板角色编码前缀；SyncRole 只动该前缀的 system 角色绑定。
const templateCodePrefix = "tenant_"

// roleSyncAdapter 把 IAM 仓储适配成租户域的 RoleSyncer 端口。
type roleSyncAdapter struct {
	roles    domain.RoleRepository
	bindings domain.BindingRepository
	ids      id.Generator
}

// 编译期断言：适配器必须满足租户域端口。
var _ app.RoleSyncer = roleSyncAdapter{}

// NewRoleSyncer 构造适配器（公共入口）：集成测试与外部装配复用同一实现；
// 生产装配经 newRoleSyncerFromDeps。三个依赖均必填。
func NewRoleSyncer(roles domain.RoleRepository, bindings domain.BindingRepository, ids id.Generator) (app.RoleSyncer, error) {
	if roles == nil || bindings == nil {
		return nil, fmt.Errorf("tenant role sync requires iam role and binding repositories")
	}
	if ids == nil {
		return nil, fmt.Errorf("tenant role sync requires an id generator")
	}
	return roleSyncAdapter{roles: roles, bindings: bindings, ids: ids}, nil
}

// EnsureTemplates 物化租户的三个模板角色；已存在即跳过（幂等）。
// 软删的同名角色视为装配缺陷（code 复用被禁止），直接报错。
func (a roleSyncAdapter) EnsureTemplates(ctx context.Context, tenantID string) error {
	for _, spec := range templates {
		existing, err := a.roles.FindByCode(ctx, domain.ChannelBiz, tenantID, spec.code)
		switch {
		case err == nil:
			if existing.IsDeleted() {
				return fmt.Errorf("tenant role sync: template role %s in %s is deleted", spec.code, tenantID)
			}
			continue
		case errors.Is(err, apperr.ErrNotFound):
			// 正常创建路径。
		default:
			return fmt.Errorf("tenant role sync: find role %s: %w", spec.code, err)
		}
		role, buildErr := domain.NewRole(domain.NewRoleParams{
			ID:          a.mustID(),
			Channel:     domain.ChannelBiz,
			Scope:       domain.RoleScopeMerchant,
			MerchantID:  tenantID,
			Code:        spec.code,
			Name:        spec.name,
			Description: spec.desc,
			Type:        domain.RoleTypeSystem,
			Actor:       roleSyncActor,
		})
		if buildErr != nil {
			return fmt.Errorf("tenant role sync: build role %s: %w", spec.code, buildErr)
		}
		if saveErr := a.roles.Save(ctx, role, 0); saveErr != nil {
			return fmt.Errorf("tenant role sync: save role %s: %w", spec.code, saveErr)
		}
	}
	return nil
}

// SyncRole 使主体的模板角色绑定与目标成员角色一致（多退少补，幂等）。
//
// role 为 nil → 解除全部模板角色绑定（成员停用）；
// role 非 nil → 目标模板之外的模板绑定解绑，缺的目标绑定补上。
// 只动本租户的模板角色绑定（system 类型 + tenant_ 前缀），
// 商户自建角色（custom）的绑定不碰。
func (a roleSyncAdapter) SyncRole(ctx context.Context, tenantID, subjectID string, role *tenantdomain.MemberRole) error {
	current, err := a.bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   subjectID,
		MerchantID:  tenantID,
	})
	if err != nil {
		return fmt.Errorf("tenant role sync: list bindings: %w", err)
	}

	// 目标角色（在册成员才有）：按 code 加载；不存在即 EnsureTemplates 未跑，
	// 属装配缺陷——绑定一个不存在的角色会被外键拒绝，宁可在这里显式失败。
	var wantID string
	if role != nil {
		target, findErr := a.roles.FindByCode(ctx, domain.ChannelBiz, tenantID, templateCodePrefix+string(*role))
		if findErr != nil {
			return fmt.Errorf("tenant role sync: find target role %s: %w", templateCodePrefix+string(*role), findErr)
		}
		wantID = target.ID
	}

	// 退：解绑所有「模板角色但非目标」的生效绑定。
	for _, binding := range current {
		if binding.RevokedAt != nil || binding.RoleID == wantID {
			continue
		}
		if !a.isTemplateBinding(ctx, binding) {
			continue
		}
		if revokeErr := a.bindings.Revoke(ctx, binding.ID, roleSyncActor, "tenant member role sync"); revokeErr != nil {
			return fmt.Errorf("tenant role sync: revoke binding %s: %w", binding.ID, revokeErr)
		}
	}
	if wantID == "" {
		return nil
	}

	// 补：目标绑定已生效则跳过（幂等）；缺失或已撤销则创建。
	for _, binding := range current {
		if binding.RoleID == wantID && binding.RevokedAt == nil {
			return nil
		}
	}
	created, buildErr := domain.NewBinding(domain.NewBindingParams{
		ID:          a.mustID(),
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   subjectID,
		MerchantID:  tenantID,
		RoleID:      wantID,
		GrantedBy:   roleSyncActor,
	})
	if buildErr != nil {
		return fmt.Errorf("tenant role sync: build binding: %w", buildErr)
	}
	if saveErr := a.bindings.Save(ctx, created); saveErr != nil {
		return fmt.Errorf("tenant role sync: save binding: %w", saveErr)
	}
	return nil
}

// isTemplateBinding 判断绑定是否指向本租户的模板角色。
//
// 角色读取失败按非模板处理会让解绑漏掉脏数据——但绑定外键保证 role 存在，
// 这里失败只可能是库异常，返回 false 并由上层调用方的下一次操作暴露。
func (a roleSyncAdapter) isTemplateBinding(ctx context.Context, binding *domain.Binding) bool {
	role, err := a.roles.FindByID(ctx, binding.RoleID)
	if err != nil {
		return false
	}
	return role.Type == domain.RoleTypeSystem &&
		role.Channel == domain.ChannelBiz &&
		role.MerchantID == binding.MerchantID &&
		len(role.Code) > len(templateCodePrefix) &&
		role.Code[:len(templateCodePrefix)] == templateCodePrefix
}

// mustID 生成绑定 / 角色 ID；生成失败走 panic——调用方（成员变更事务）
// 无法从「没有 ID」恢复，快速失败让 UoW 回滚比返回半一致状态更安全。
func (a roleSyncAdapter) mustID() string {
	generated, err := a.ids.NextString()
	if err != nil {
		panic(fmt.Sprintf("tenant role sync: generate id: %v", err))
	}
	return generated
}

// templates 是模板角色静态定义；权限授予由迁移 000015 承载
// （运行时物化只建角色——权限演进走 IAM 既有变更流程，不在同步范围内）。
var templates = []struct {
	code string
	name string
	desc string
}{
	{templateCodePrefix + "owner", "租户所有者", "系统内置：owner 成员的默认角色（ADR-0007）"},
	{templateCodePrefix + "admin", "租户管理员", "系统内置：admin 成员的默认角色（ADR-0007）"},
	{templateCodePrefix + "member", "租户成员", "系统内置：member 成员的默认角色（ADR-0007）"},
}

// SCIM 组 → IAM 角色投影的装配实现（docs/identity-authn.md §21.1.3「角色投影」）。
//
// 依赖方向：身份域只定义 ScimRoleProvisioner 端口，本文件在 cmd 层实现它，
// 身份域不 import IAM（§13.1）。绑定写入复用 BindingService，
// 保证与手工授权走同一套唯一键、审计（rbac.binding_change）与额度校验。

package iamruntime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformaudit "github.com/admin-frame/admin-frame/internal/platform/audit"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// scimBindingReason 是 SCIM 投影授权的审计原因；不包含组名（可能是 PII 线索）。
const scimBindingReason = "scim group mapping"

// scimRevokeReasonMaxLen 与 iam_role_binding.revoke_reason 列宽一致（VARCHAR(512)）。
const scimRevokeReasonMaxLen = 512

// ScimRoleProvisioner 把 SCIM 组成员关系投影为 A 端 staff 角色绑定。
//
// 语义：
//   - 只处理 channel=admin、subject_type=staff、merchant_id="" 的绑定；
//   - Grant 幂等（BindingService.Bind 命中唯一键时直接返回既有绑定）；
//   - Revoke 只撤销该账号在该角色上的生效绑定；
//   - 不注入 snapshot invalidator：IAM 快照 TTL ≤60s，符合 §21.1.3 的时效要求。
type ScimRoleProvisioner struct {
	roles    domain.RoleRepository
	bindings domain.BindingRepository
	service  *app.BindingService
	logger   *slog.Logger
}

// NewScimRoleProvisioner 构造投影实现；仅 A 端允许。
//
// 与 IAM Runtime 独立装配（同一 DB、独立服务实例）：身份域运行时先于 IAM 运行时构造，
// 不能反向依赖 Runtime 字段。
func NewScimRoleProvisioner(deps *platform.Deps, iamCfg iamconfig.IAMConfig) (*ScimRoleProvisioner, error) {
	if deps == nil || deps.Config == nil {
		return nil, apperr.Internal("scim role provisioner requires platform deps", nil)
	}
	if deps.Config.Channel != "admin" {
		return nil, apperr.Internal("scim role provisioner is only supported on admin channel", nil)
	}
	roles, bindings, _, _, err := BuildRepositories(deps)
	if err != nil {
		return nil, err
	}
	if deps.IDs == nil {
		return nil, apperr.Internal("scim role provisioner requires id generator", nil)
	}
	recorder := ChangeRecorderFor(deps)
	uow := app.UnitOfWork(app.NoopUnitOfWork{})
	if deps.Database != nil {
		uow = persistence.NewSQLUnitOfWork(deps.Database.SQL())
	}
	service, err := app.NewBindingService(app.BindingServiceConfig{
		Roles:       roles,
		Bindings:    bindings,
		Recorder:    recorder,
		UnitOfWork:  uow,
		IDs:         deps.IDs,
		Audit:       app.AuditMeta{Service: deps.Config.Service, ActorType: "staff"},
		Limits:      app.BindingLimits{MaxBindingsPerSubject: iamCfg.Limits.MaxBindingsPerSubject},
		Constraints: nil, // SCIM 以 IdP 为权威：RBAC2 冲突由 IAM 复核覆盖，不在同步热路径拒绝
		Logger:      deps.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("scim role provisioner: %w", err)
	}
	return &ScimRoleProvisioner{
		roles: roles, bindings: bindings, service: service, logger: deps.Logger,
	}, nil
}

// ChangeRecorderFor 返回与 IAM Runtime 一致的变更审计出口。
//
// async 启用时写 Outbox（崩溃不丢审计），否则直接落结构化日志。
func ChangeRecorderFor(deps *platform.Deps) app.ChangeRecorder {
	emitter := platformaudit.SlogEmitter{Log: deps.Logger}
	if deps.Async != nil {
		return app.OutboxRecorder{Events: deps.Async.Store()}
	}
	return app.SlogRecorder{Emitter: emitter}
}

// GrantGroupRoles 为账号绑定指定角色；重复绑定幂等。
func (p *ScimRoleProvisioner) GrantGroupRoles(ctx context.Context, accountID, actor string, roleCodes []string) error {
	if len(roleCodes) == 0 {
		return nil
	}
	for _, code := range roleCodes {
		role, err := p.resolveRole(ctx, code)
		if err != nil {
			return err
		}
		if _, bindErr := p.service.Bind(ctx, app.BindCommand{
			Channel:     domain.ChannelAdmin,
			SubjectType: domain.SubjectStaff,
			SubjectID:   strings.TrimSpace(accountID),
			MerchantID:  "",
			RoleID:      role.ID,
			Actor:       actor,
			Reason:      scimBindingReason,
		}); bindErr != nil {
			return fmt.Errorf("scim grant role %s: %w", code, bindErr)
		}
	}
	return nil
}

// RevokeGroupRoles 撤销账号在指定角色上的绑定；不存在视为幂等成功。
func (p *ScimRoleProvisioner) RevokeGroupRoles(
	ctx context.Context,
	accountID, actor, reason string,
	roleCodes []string,
) error {
	if len(roleCodes) == 0 {
		return nil
	}
	bindings, err := p.bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   strings.TrimSpace(accountID),
		MerchantID:  "",
	})
	if err != nil {
		return fmt.Errorf("scim list bindings: %w", err)
	}
	byRole := make(map[string]*domain.Binding, len(bindings))
	for _, binding := range bindings {
		byRole[binding.RoleID] = binding
	}
	for _, code := range roleCodes {
		role, resolveErr := p.resolveRole(ctx, code)
		if resolveErr != nil {
			// 角色已被删除：绑定也已随角色失效，按幂等处理，避免阻塞成员移除。
			if p.logger != nil {
				p.logger.WarnContext(ctx, "scim revoke skipped for unresolvable role",
					"role_code", code, "error", resolveErr.Error())
			}
			continue
		}
		binding, ok := byRole[role.ID]
		if !ok {
			continue
		}
		if unbindErr := p.service.Unbind(ctx, app.UnbindCommand{
			ID:     binding.ID,
			Actor:  actor,
			Reason: scimRevokeReason(reason),
		}); unbindErr != nil {
			return fmt.Errorf("scim revoke role %s: %w", code, unbindErr)
		}
	}
	return nil
}

// ValidateRoleCodes 在装配期校验配置的角色可用：存在、A 端、未停用、非破玻璃。
func (p *ScimRoleProvisioner) ValidateRoleCodes(ctx context.Context, roleCodes []string) error {
	for _, code := range roleCodes {
		if _, err := p.resolveRole(ctx, code); err != nil {
			return err
		}
	}
	return nil
}

// resolveRole 解析角色并校验可用性；任何不可用都返回错误而不是降级。
func (p *ScimRoleProvisioner) resolveRole(ctx context.Context, code string) (*domain.Role, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return nil, apperr.BadRequest("scim role code is required")
	}
	role, err := p.roles.FindByCode(ctx, domain.ChannelAdmin, "", trimmed)
	if err != nil {
		return nil, fmt.Errorf("scim role %s is not available: %w", trimmed, err)
	}
	if role.IsDeleted() {
		return nil, fmt.Errorf("scim role %s is deleted", trimmed)
	}
	if role.Status != domain.RoleStatusActive {
		return nil, fmt.Errorf("scim role %s is not active", trimmed)
	}
	if role.Type == domain.RoleTypeBreakglass {
		// 破玻璃角色必须走审批，禁止由 IdP 组自动授予。
		return nil, fmt.Errorf("scim role %s must not be a breakglass role", trimmed)
	}
	return role, nil
}

// scimRevokeReason 生成解绑原因；截断到列宽内并保留可追溯前缀。
func scimRevokeReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return scimBindingReason
	}
	if len(trimmed) > scimRevokeReasonMaxLen {
		return trimmed[:scimRevokeReasonMaxLen]
	}
	return trimmed
}

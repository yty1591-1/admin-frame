package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// BindingService 是主体-角色绑定用例。
//
// 绑定是提权的直接通道：端别、主体类型、商户三者必须与角色完全一致，
// 且破玻璃角色必须带有效期；任何不匹配都拒绝而不是「就近修正」。
type BindingService struct {
	roles       domain.RoleRepository
	bindings    domain.BindingRepository
	recorder    ChangeRecorder
	invalidator SnapshotInvalidator
	uow         UnitOfWork
	log         *slog.Logger
	ids         id.Generator
	audit       auditBuilder
	limits      BindingLimits
}

// BindingLimits 是绑定相关上限与安全开关。
type BindingLimits struct {
	// MaxBindingsPerSubject 单主体最大绑定数。
	MaxBindingsPerSubject int
	// RequireReason 解绑是否强制填写原因。
	RequireReason bool
}

// BindingServiceConfig 是 BindingService 的装配参数。
type BindingServiceConfig struct {
	Roles       domain.RoleRepository
	Bindings    domain.BindingRepository
	Recorder    ChangeRecorder
	Invalidator SnapshotInvalidator
	// UnitOfWork 让「绑定写入 + 审计事件」同事务提交；nil 时退化为无事务。
	UnitOfWork UnitOfWork
	IDs        id.Generator
	Audit      AuditMeta
	Limits     BindingLimits
	Logger     *slog.Logger
}

// NewBindingService 构造绑定用例；缺失必要依赖时返回错误（fail-closed）。
func NewBindingService(cfg BindingServiceConfig) (*BindingService, error) {
	if cfg.Roles == nil {
		return nil, apperr.Internal("iam role repository is required", nil)
	}
	if cfg.Bindings == nil {
		return nil, apperr.Internal("iam binding repository is required", nil)
	}
	if cfg.Recorder == nil {
		return nil, apperr.Internal("iam change recorder is required", nil)
	}
	if cfg.IDs == nil {
		cfg.IDs = id.MustSnowflake(id.Options{NodeID: 1})
	}
	if cfg.Limits.MaxBindingsPerSubject <= 0 {
		cfg.Limits.MaxBindingsPerSubject = 50
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	uow := cfg.UnitOfWork
	if uow == nil {
		uow = NoopUnitOfWork{}
	}
	return &BindingService{
		roles:       cfg.Roles,
		bindings:    cfg.Bindings,
		recorder:    cfg.Recorder,
		invalidator: cfg.Invalidator,
		uow:         uow,
		log:         logger,
		ids:         cfg.IDs,
		audit:       newAuditBuilder(cfg.IDs, cfg.Audit),
		limits:      cfg.Limits,
	}, nil
}

// BindCommand 是绑定角色入参。
type BindCommand struct {
	Channel     domain.Channel
	SubjectType domain.SubjectType
	SubjectID   string
	MerchantID  string
	RoleID      string
	ExpiresAt   *time.Time
	// AllowBreakglass 仅供破玻璃审批执行器使用：为 true 才允许绑定 breakglass 角色。
	//
	// 该字段不对传输层暴露：普通「创建绑定」接口永远走审批，不能直接拿到超级权限。
	AllowBreakglass bool
	Actor           string
	Reason          string
}

// Bind 绑定主体与角色；重复绑定幂等并返回既有绑定。
func (s *BindingService) Bind(ctx context.Context, cmd BindCommand) (*domain.Binding, error) {
	if strings.TrimSpace(cmd.Actor) == "" {
		return nil, apperr.BadRequest("actor is required")
	}
	role, err := s.roles.FindByID(ctx, cmd.RoleID)
	if err != nil {
		return nil, err
	}
	if role.IsDeleted() {
		return nil, apperr.NotFound("role not found")
	}
	if role.Type == domain.RoleTypeBreakglass && !cmd.AllowBreakglass {
		return nil, apperr.Forbidden("breakglass binding requires approval")
	}

	existing, err := s.bindings.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     cmd.Channel,
		SubjectType: cmd.SubjectType,
		SubjectID:   cmd.SubjectID,
		MerchantID:  cmd.MerchantID,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	effective := 0
	for _, binding := range existing {
		if !binding.IsEffective(now) {
			continue
		}
		if binding.RoleID == cmd.RoleID {
			// 幂等：同一 (主体, 角色) 重复绑定直接返回既有记录，不消耗额度。
			return binding, nil
		}
		effective++
	}
	if effective >= s.limits.MaxBindingsPerSubject {
		return nil, apperr.TooManyRequests("subject binding limit reached")
	}

	bindingID, err := s.ids.NextString()
	if err != nil {
		return nil, apperr.Internal("generate binding id", err)
	}
	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID:          bindingID,
		Channel:     cmd.Channel,
		SubjectType: cmd.SubjectType,
		SubjectID:   cmd.SubjectID,
		MerchantID:  cmd.MerchantID,
		RoleID:      cmd.RoleID,
		ExpiresAt:   cmd.ExpiresAt,
		GrantedBy:   cmd.Actor,
	})
	if err != nil {
		return nil, err
	}
	if err := binding.ValidateAgainst(role, now); err != nil {
		return nil, err
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.bindings.Save(ctx, binding); err != nil {
			return err
		}
		// 过期后重新绑定会命中唯一键：仓储刷新既有记录而保留原 ID，
		// 因此回读一次拿真实主键，避免返回一个无法解绑的临时 ID。
		// 回读与写入同事务，读到的是本次写入后的最新状态。
		if persisted, listErr := s.bindings.ListBySubject(ctx, domain.SubjectQuery{
			Channel:     cmd.Channel,
			SubjectType: cmd.SubjectType,
			SubjectID:   cmd.SubjectID,
			MerchantID:  cmd.MerchantID,
		}); listErr == nil {
			for _, candidate := range persisted {
				if candidate.RoleID == cmd.RoleID {
					binding = candidate
					break
				}
			}
		}
		attrs := map[string]string{
			attrRoleID:      role.ID,
			attrRoleCode:    role.Code,
			attrSubjectType: string(binding.SubjectType),
			attrSubjectID:   binding.SubjectID,
		}
		if binding.ExpiresAt != nil {
			attrs[attrExpiresAt] = binding.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return s.recordBinding(ctx, binding, opBind, cmd.Actor, cmd.Reason, attrs)
	}); err != nil {
		return nil, err
	}
	s.invalidate(ctx, binding)
	return binding, nil
}

// UnbindCommand 是解绑入参。
type UnbindCommand struct {
	ID         string
	MerchantID string
	Actor      string
	Reason     string
}

// Unbind 解绑；不存在的绑定按幂等成功处理（不写审计，因为没有可归属的事实）。
func (s *BindingService) Unbind(ctx context.Context, cmd UnbindCommand) error {
	if s.limits.RequireReason && strings.TrimSpace(cmd.Reason) == "" {
		return apperr.BadRequest("reason is required")
	}
	binding, err := s.bindings.FindByID(ctx, cmd.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// 幂等解绑：不存在的 ID 视为已完成，避免重试报错。
			return nil
		}
		return err
	}
	if cmd.MerchantID != "" && binding.MerchantID != cmd.MerchantID {
		// 跨商户一律 404，不泄露资源是否存在（SEC-14）。
		return apperr.NotFound("binding not found")
	}
	if err := s.uow.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.bindings.Delete(ctx, binding.ID); err != nil {
			return err
		}
		return s.recordBinding(ctx, binding, opUnbind, cmd.Actor, cmd.Reason, map[string]string{
			attrRoleID:      binding.RoleID,
			attrSubjectType: string(binding.SubjectType),
			attrSubjectID:   binding.SubjectID,
		})
	}); err != nil {
		return err
	}
	s.invalidate(ctx, binding)
	return nil
}

// recordBinding 写入绑定变更审计（action=rbac.binding_change）。
func (s *BindingService) recordBinding(ctx context.Context, binding *domain.Binding, operation, actor, reason string, attrs map[string]string) error {
	if attrs == nil {
		attrs = make(map[string]string, 3)
	}
	attrs[attrOperation] = operation
	return s.audit.record(ctx, s.recorder, auditParams{
		action:       audit.ActionRBACBindingChange,
		actor:        actor,
		channel:      string(binding.Channel),
		merchantID:   binding.MerchantID,
		resourceType: ResourceTypeBinding,
		resourceID:   binding.ID,
		outcome:      audit.OutcomeSuccess,
		reason:       reason,
		attrs:        attrs,
	})
}

// invalidate 失效主体快照；失败只记录，TTL 兜底。
func (s *BindingService) invalidate(ctx context.Context, binding *domain.Binding) {
	if s.invalidator == nil {
		return
	}
	if err := s.invalidator.InvalidateSubject(ctx, string(binding.Channel), binding.MerchantID,
		string(binding.SubjectType), binding.SubjectID); err != nil {
		s.log.WarnContext(ctx, "iam invalidate subject cache failed",
			"binding_id", binding.ID, "subject_id", binding.SubjectID, "err", err)
	}
}

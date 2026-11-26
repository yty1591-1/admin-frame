// Package app 是配置中心用例层（M1-2 核心逻辑）。
//
// 职责边界：
//   - 管理面写入一律以代码 key spec（internal/configcenter/spec）为准，
//     未声明键拒绝写入；
//   - 值是只追加版本（config_item_version），元数据用乐观锁 CAS；
//   - 发布是不可变快照 + 指针 CAS，回滚生成新 Release 指向历史快照；
//   - 审计事件走 internal/shared/audit 端口，Outbox 出口 fail closed；
//   - 事务由调用方（transport 层）通过 pkg/db.WithinUnitOfWork 包裹，
//     本层只保证同一 ctx 内复用事务。
package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 审计资源类型（docs/audit/EVENTS.md config.* 段）。
const (
	ResourceTypeItem    = "config_item"
	ResourceTypeRelease = "config_release"
)

// RoleCodeSystemAdmin 是引导用超级管理员角色编码（migrations/000005 system_admin）。
const RoleCodeSystemAdmin = "system_admin"

// Actor 是审计主体（与 featureflag / tenant 用例层形态一致）。
type Actor struct {
	SubjectID  string
	ActorType  string
	Channel    string
	MerchantID string
	TraceID    string
	// Roles 来自 IAM 快照（优先）或令牌声明；用于 system_admin 跳过双人审批。
	Roles []string
}

// IsSystemAdmin 报告主体是否持有超级管理员角色（可跳过配置/密钥双人审批，仍写审计）。
func (a Actor) IsSystemAdmin() bool {
	for _, role := range a.Roles {
		if role == RoleCodeSystemAdmin {
			return true
		}
	}
	return false
}

// Options 是用例公共参数。
type Options struct {
	// Service 服务名，写入审计事件。
	Service string
	// Now 时钟；测试注入。
	Now func() time.Time
	// SecretObserver 密钥解析指标；nil 表示不打点。
	SecretObserver SecretResolveObserver
	// Specs 键声明注册表；Release/Secret 发布与入库校验使用（CC-R-07/14）。
	Specs *spec.Registry
}

type auditContext struct {
	emitter audit.Emitter
	ids     id.Generator
	service string
	now     func() time.Time
}

// emit 写一条审计事件；返回错误由调用方决定回滚（Outbox 模式 fail closed）。
func (a auditContext) emit(ctx context.Context, actor Actor, action, resourceType, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) error {
	eventID := ""
	if a.ids != nil {
		if generated, err := a.ids.NextString(); err == nil {
			eventID = generated
		}
	}
	if eventID == "" {
		eventID = "configcenter-" + a.now().UTC().Format("20060102150405.000000000")
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actor.SubjectID,
		ActorType:    actorType(actor),
		Channel:      channel(actor.Channel),
		MerchantID:   actor.MerchantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   a.now().UTC(),
		TraceID:      actor.TraceID,
		Service:      a.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	return a.emitter.Emit(ctx, event)
}

func actorType(a Actor) string {
	if strings.TrimSpace(a.ActorType) == "" {
		return "user"
	}
	return a.ActorType
}

// channel 归一端别；配置管理仅 A 端，空值视为 admin。
func channel(c string) string {
	if strings.TrimSpace(c) == "" {
		return "admin"
	}
	return c
}

// requireActor 校验审计主体；缺失直接拒绝，避免产生无法追责的写入。
func requireActor(a Actor) error {
	if strings.TrimSpace(a.SubjectID) == "" {
		return apperr.Unauthorized("actor is required")
	}
	return nil
}

// translate 把领域哨兵错误映射为统一应用错误。
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound("config center resource not found")
	case errors.Is(err, domain.ErrDuplicateKey):
		return apperr.Conflict("config center resource already exists")
	case errors.Is(err, domain.ErrVersionConflict):
		return apperr.Conflict("config center resource was modified by another request")
	case errors.Is(err, domain.ErrStateConflict):
		return apperr.Conflict("config center resource state does not allow this operation")
	case errors.Is(err, domain.ErrPointerConflict):
		return apperr.Conflict("active release was switched by another request")
	default:
		return apperr.Internal("config center operation failed", err)
	}
}

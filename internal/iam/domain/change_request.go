package domain

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const (
	// MaxChangeReasonLen 审批单原因最大字节数，与 iam_change_request.reason 列宽一致。
	MaxChangeReasonLen = 512
	// MaxChangeTargetIDLen 目标 ID 最大字节数，与 iam_change_request.target_id 列宽一致。
	MaxChangeTargetIDLen = 64
	// MaxChangePayloadBytes 审批载荷上限（16 KiB）。
	//
	// 载荷只是「待审批的意图」，不是审计归档：超限通常意味着把整份角色清单塞进来，
	// 既放大了审批人误判的风险，也会让列表接口变慢。需要回溯时按 target_id 查目标聚合。
	MaxChangePayloadBytes = 16 << 10
)

// ChangeStatus 是审批单状态机取值。
//
// 状态转移只允许：
//
//	pending → approved → applied
//	pending → rejected
//	pending → cancelled
//
// 任何终态都不可回退；重复提交同一决定返回 40900 而不是静默成功，
// 避免审批人误以为「再点一次」能撤销上一次决定。
type ChangeStatus string

const (
	// ChangeStatusPending 待审批。
	ChangeStatusPending ChangeStatus = "pending"
	// ChangeStatusApproved 已通过，等待应用。
	ChangeStatusApproved ChangeStatus = "approved"
	// ChangeStatusRejected 已拒绝。
	ChangeStatusRejected ChangeStatus = "rejected"
	// ChangeStatusApplied 已应用，目标聚合已落库。
	ChangeStatusApplied ChangeStatus = "applied"
	// ChangeStatusCancelled 申请人已撤销。
	ChangeStatusCancelled ChangeStatus = "cancelled"
)

// Validate 校验状态取值。
func (s ChangeStatus) Validate() error {
	switch s {
	case ChangeStatusPending, ChangeStatusApproved, ChangeStatusRejected,
		ChangeStatusApplied, ChangeStatusCancelled:
		return nil
	default:
		return errInvalid(ErrInvalidChangeStatus, "change status %q is not supported", string(s))
	}
}

// IsTerminal 报告状态是否已不可继续流转。
func (s ChangeStatus) IsTerminal() bool {
	return s == ChangeStatusRejected || s == ChangeStatusApplied || s == ChangeStatusCancelled
}

// ChangeAction 是审批单覆盖的高风险动作。
//
// 只有会扩大权限或改变授权关系的动作才进入审批；读接口与模拟鉴权不需要。
type ChangeAction string

const (
	// ChangeActionRoleCreate 创建角色。
	ChangeActionRoleCreate ChangeAction = "role_create"
	// ChangeActionRoleUpdate 修改角色名称 / 说明 / 启停。
	ChangeActionRoleUpdate ChangeAction = "role_update"
	// ChangeActionRoleDelete 软删角色。
	ChangeActionRoleDelete ChangeAction = "role_delete"
	// ChangeActionRoleClone 克隆角色。
	ChangeActionRoleClone ChangeAction = "role_clone"
	// ChangeActionRolePermissionReplace 全量替换角色权限。
	ChangeActionRolePermissionReplace ChangeAction = "role_permission_replace"
	// ChangeActionBindingCreate 绑定角色。
	ChangeActionBindingCreate ChangeAction = "binding_create"
	// ChangeActionBindingDelete 解绑角色。
	ChangeActionBindingDelete ChangeAction = "binding_delete"
	// ChangeActionBreakglassRequest 申请限时超级权限。
	ChangeActionBreakglassRequest ChangeAction = "breakglass_request"
)

// ChangeTargetType 是审批单指向的目标聚合类型，与 iam_change_request.target_type 一致。
type ChangeTargetType string

const (
	// ChangeTargetIAMRole IAM 角色。
	ChangeTargetIAMRole ChangeTargetType = "iam_role"
	// ChangeTargetIAMBbinding IAM 角色绑定。
	ChangeTargetIAMBbinding ChangeTargetType = "iam_binding"
	// ChangeTargetIAMBreakglass 破玻璃授权。
	ChangeTargetIAMBreakglass ChangeTargetType = "iam_breakglass"
)

// TargetType 返回动作对应的目标类型；未知动作返回 40000。
func (a ChangeAction) TargetType() (ChangeTargetType, error) {
	switch a {
	case ChangeActionRoleCreate, ChangeActionRoleUpdate, ChangeActionRoleDelete,
		ChangeActionRoleClone, ChangeActionRolePermissionReplace:
		return ChangeTargetIAMRole, nil
	case ChangeActionBindingCreate, ChangeActionBindingDelete:
		return ChangeTargetIAMBbinding, nil
	case ChangeActionBreakglassRequest:
		return ChangeTargetIAMBreakglass, nil
	default:
		return "", errInvalid(ErrInvalidChangeAction, "change action %q is not supported", string(a))
	}
}

// allowsEmptyTarget 报告该动作在申请阶段是否允许没有目标 ID。
//
// 新建类动作在应用前还没有业务主键，审批单只承载载荷；删除 / 变更类动作
// 必须指向已存在的聚合，否则审批人无法判断影响面。
func (a ChangeAction) allowsEmptyTarget() bool {
	switch a {
	case ChangeActionRoleCreate, ChangeActionRoleClone,
		ChangeActionBindingCreate, ChangeActionBreakglassRequest:
		return true
	default:
		return false
	}
}

// Validate 校验动作取值。
func (a ChangeAction) Validate() error {
	_, err := a.TargetType()
	return err
}

// changePayloadForbiddenKeys 是载荷中禁止出现的敏感字段（AUD-08 / SEC-05）。
//
// 审批载荷会进入审计与人工复核视图，Token / 密钥一旦写入就无法撤回；
// 这里在领域层直接拒绝，而不是依赖调用方自觉。
var changePayloadForbiddenKeys = map[string]struct{}{
	"token":          {},
	"access_token":   {},
	"refresh_token":  {},
	"authorization":  {},
	"password":       {},
	"secret":         {},
	"client_secret":  {},
	"private_key":    {},
	"signing_key":    {},
	"session_cookie": {},
}

// ChangeRequest 是高风险 IAM 变更的审批单聚合根。
//
// 不变量：
//   - 申请人必须存在，审批人不得与申请人相同（职责分离，SoD）；
//   - 载荷必须是 JSON 对象，不含 Token / 密钥，且不超过 16 KiB；
//   - 删除 / 变更类动作必须携带目标 ID；新建类动作允许为空；
//   - 状态只能按 pending → approved → applied / rejected / cancelled 流转；
//   - 每次决定递增 version，持久化必须带 expectedVersion 做乐观锁。
//
// 审批单只是「意图」，应用前必须按 target_id 重新加载目标聚合并重新校验权限，
// 不得直接信任载荷（载荷可能已过期或与当前状态冲突）。
type ChangeRequest struct {
	// ID 审批单主键（雪花 ID）。
	ID string
	// Channel 发起端别。
	Channel Channel
	// Action 申请动作。
	Action ChangeAction
	// TargetType 目标聚合类型，由 Action 派生。
	TargetType ChangeTargetType
	// TargetID 目标聚合 ID；新建类动作可为空串。
	TargetID string
	// Payload 待审批的变更载荷（JSON 对象），保持原始字节以便审计比对。
	Payload []byte
	// Status 状态机当前状态。
	Status ChangeStatus
	// Reason 申请原因；是否必填由 app 层按配置决定。
	Reason string
	// RequestedBy 申请人 subject_id。
	RequestedBy string
	// RequestedAt 申请时间（UTC）。
	RequestedAt time.Time
	// ApprovedBy 决定人 subject_id（通过或拒绝都记录）。
	ApprovedBy string
	// ApprovedAt 决定时间（UTC）；未决定为 nil。
	ApprovedAt *time.Time
	// AppliedAt 实际应用时间（UTC）；未应用为 nil。
	AppliedAt *time.Time
	// Version 乐观锁版本，初始为 1。
	Version int64
}

// NewChangeRequestParams 创建审批单的入参。
type NewChangeRequestParams struct {
	ID       string
	Channel  Channel
	Action   ChangeAction
	TargetID string
	Payload  []byte
	Reason   string
	Actor    string
}

// NewChangeRequest 创建处于 pending 状态、版本为 1 的审批单。
func NewChangeRequest(p NewChangeRequestParams) (*ChangeRequest, error) {
	if p.ID == "" {
		return nil, errInvalid(ErrEmptyInput, "change request id is required")
	}
	if p.Actor == "" {
		return nil, errInvalid(ErrMissingActor, "change request requester is required")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if err := p.Action.Validate(); err != nil {
		return nil, err
	}
	targetType, err := p.Action.TargetType()
	if err != nil {
		return nil, err
	}
	if err := validateChangeTarget(p.Action, p.TargetID); err != nil {
		return nil, err
	}
	payload, err := normalizeChangePayload(p.Channel, p.Payload)
	if err != nil {
		return nil, err
	}
	reason, err := normalizeChangeReason(p.Reason)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	return &ChangeRequest{
		ID:          p.ID,
		Channel:     p.Channel,
		Action:      p.Action,
		TargetType:  targetType,
		TargetID:    p.TargetID,
		Payload:     payload,
		Status:      ChangeStatusPending,
		Reason:      reason,
		RequestedBy: p.Actor,
		RequestedAt: now,
		Version:     1,
	}, nil
}

// IsPending 报告审批单是否仍可被决定。
func (c *ChangeRequest) IsPending() bool { return c.Status == ChangeStatusPending }

// IsApproved 报告审批单是否已通过但尚未应用。
func (c *ChangeRequest) IsApproved() bool { return c.Status == ChangeStatusApproved }

// DecodePayload 把载荷解码到目标结构；调用方仍需重新校验业务规则。
func (c *ChangeRequest) DecodePayload(dst any) error {
	if dst == nil {
		return errInvalid(ErrEmptyInput, "decode target is required")
	}
	if err := json.Unmarshal(c.Payload, dst); err != nil {
		return errInvalid(ErrChangePayloadInvalid, "change payload is not a valid JSON object: %v", err)
	}
	return nil
}

// Approve 通过审批；审批人必须与申请人不同。
func (c *ChangeRequest) Approve(approver string, expectedVersion int64) error {
	if err := c.ensurePending(approver, expectedVersion); err != nil {
		return err
	}
	if approver == c.RequestedBy {
		return errForbidden(ErrSelfApproval,
			"requester %s must not approve their own change request (separation of duties)", approver)
	}
	now := nowUTC()
	c.Status = ChangeStatusApproved
	c.ApprovedBy = approver
	c.ApprovedAt = &now
	c.Version++
	return nil
}

// Reject 拒绝审批；同样适用职责分离，避免申请人绕过审批自行关闭。
func (c *ChangeRequest) Reject(approver string, expectedVersion int64) error {
	if err := c.ensurePending(approver, expectedVersion); err != nil {
		return err
	}
	if approver == c.RequestedBy {
		return errForbidden(ErrSelfApproval,
			"requester %s must not reject their own change request", approver)
	}
	now := nowUTC()
	c.Status = ChangeStatusRejected
	c.ApprovedBy = approver
	c.ApprovedAt = &now
	c.Version++
	return nil
}

// Cancel 由申请人撤销待审批单；已决定的单子不可撤销。
func (c *ChangeRequest) Cancel(actor string, expectedVersion int64) error {
	if err := c.ensurePending(actor, expectedVersion); err != nil {
		return err
	}
	if actor != c.RequestedBy {
		return errForbidden(ErrNotRequester,
			"only requester %s may cancel change request %s", c.RequestedBy, c.ID)
	}
	c.Status = ChangeStatusCancelled
	c.Version++
	return nil
}

// MarkApplied 标记审批单已应用。
//
// 应用动作必须幂等：重复调用返回 40900，由调用方回读状态，避免同一审批单被
// 应用到两个不同版本的目标聚合。
func (c *ChangeRequest) MarkApplied(expectedVersion int64) error {
	if c.Status != ChangeStatusApproved {
		return errConflict(ErrChangeNotApproved,
			"change request %s is %s; only approved requests can be applied", c.ID, string(c.Status))
	}
	if expectedVersion != c.Version {
		return errConflict(ErrVersionConflict,
			"change request %s version %d does not match expected %d", c.ID, c.Version, expectedVersion)
	}
	now := nowUTC()
	c.Status = ChangeStatusApplied
	c.AppliedAt = &now
	c.Version++
	return nil
}

// ensurePending 校验状态与乐观锁，并统一要求决定人存在。
func (c *ChangeRequest) ensurePending(actor string, expectedVersion int64) error {
	if actor == "" {
		return errInvalid(ErrMissingActor, "actor is required for change request %s", c.ID)
	}
	if c.Status != ChangeStatusPending {
		return errConflict(ErrChangeNotPending,
			"change request %s is %s, not pending", c.ID, string(c.Status))
	}
	if expectedVersion != c.Version {
		return errConflict(ErrVersionConflict,
			"change request %s version %d does not match expected %d", c.ID, c.Version, expectedVersion)
	}
	return nil
}

// validateChangeTarget 校验目标类型与 ID 的组合。
func validateChangeTarget(action ChangeAction, targetID string) error {
	if len(targetID) > MaxChangeTargetIDLen {
		return errInvalid(ErrInvalidChangeTarget,
			"change target id exceeds %d bytes", MaxChangeTargetIDLen)
	}
	if targetID == "" && !action.allowsEmptyTarget() {
		return errInvalid(ErrInvalidChangeTarget,
			"change action %q requires a target id", string(action))
	}
	return nil
}

// normalizeChangeReason 去除首尾空白并校验长度。
func normalizeChangeReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > MaxChangeReasonLen {
		return "", errInvalid(ErrEmptyInput,
			"change reason exceeds %d bytes", MaxChangeReasonLen)
	}
	return reason, nil
}

// normalizeChangePayload 校验载荷为 JSON 对象，并拒绝敏感字段与租户覆盖。
func normalizeChangePayload(channel Channel, payload []byte) ([]byte, error) {
	if len(payload) > MaxChangePayloadBytes {
		return nil, errInvalid(ErrChangePayloadInvalid,
			"change payload exceeds %d bytes", MaxChangePayloadBytes)
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if !json.Valid(trimmed) {
		return nil, errInvalid(ErrChangePayloadInvalid, "change payload is not valid JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, errInvalid(ErrChangePayloadInvalid,
			"change payload must be a JSON object: %v", err)
	}
	for key := range fields {
		if _, forbidden := changePayloadForbiddenKeys[strings.ToLower(key)]; forbidden {
			return nil, errInvalid(ErrChangePayloadInvalid,
				"change payload must not contain sensitive field %q", key)
		}
		// B 端商户只能来自 Principal：载荷自带 merchant_id 说明调用方试图越权，
		// 直接拒绝而不是忽略，避免「看起来生效了、实际没生效」的错觉（SEC-14）。
		if channel == ChannelBiz && strings.EqualFold(key, "merchant_id") {
			return nil, errForbidden(ErrChangeTenantOverride,
				"channel biz change payload must not carry merchant_id; it is derived from the principal")
		}
	}
	return slices.Clone(trimmed), nil
}

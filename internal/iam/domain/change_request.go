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
	// MaxChangeMerchantIDLen 商户 ID 最大字节数，与 iam_change_request.merchant_id 列宽一致。
	MaxChangeMerchantIDLen = 64
	// MaxDecisionReasonLen 审批结论说明最大字节数，与 iam_change_request.decision_reason 列宽一致。
	MaxDecisionReasonLen = 512
	// ChangePayloadVersion 当前载荷结构版本；载荷语义变更时递增。
	ChangePayloadVersion = 1
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
	// ChangeStatusExpired 审批时效已过，不能再通过。
	ChangeStatusExpired ChangeStatus = "expired"
)

// Validate 校验状态取值。
func (s ChangeStatus) Validate() error {
	switch s {
	case ChangeStatusPending, ChangeStatusApproved, ChangeStatusRejected,
		ChangeStatusApplied, ChangeStatusCancelled, ChangeStatusExpired:
		return nil
	default:
		return errInvalid(ErrInvalidChangeStatus, "change status %q is not supported", string(s))
	}
}

// IsTerminal 报告状态是否已不可继续流转。
func (s ChangeStatus) IsTerminal() bool {
	return s == ChangeStatusRejected || s == ChangeStatusApplied ||
		s == ChangeStatusCancelled || s == ChangeStatusExpired
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
	// ChangeActionSodGroupCreate 创建互斥角色组（RBAC2）。
	ChangeActionSodGroupCreate ChangeAction = "sod_group_create"
	// ChangeActionSodGroupUpdate 修改互斥组名称 / 阈值 / 成员 / 启停。
	ChangeActionSodGroupUpdate ChangeAction = "sod_group_update"
	// ChangeActionSodGroupDelete 软删互斥组。
	ChangeActionSodGroupDelete ChangeAction = "sod_group_delete"
	// ChangeActionRoleConstraintUpdate 修改角色 RBAC2 约束（基数 + 前置角色）。
	ChangeActionRoleConstraintUpdate ChangeAction = "role_constraint_update"
	// ChangeActionRoleTemplateCreate 创建角色模板基线。
	ChangeActionRoleTemplateCreate ChangeAction = "role_template_create"
	// ChangeActionRoleTemplateUpdate 修改模板名称 / 说明 / 启停。
	ChangeActionRoleTemplateUpdate ChangeAction = "role_template_update"
	// ChangeActionRoleTemplateDelete 软删角色模板。
	ChangeActionRoleTemplateDelete ChangeAction = "role_template_delete"
	// ChangeActionRoleTemplatePermissionReplace 全量替换模板权限基线。
	ChangeActionRoleTemplatePermissionReplace ChangeAction = "role_template_permission_replace"
	// ChangeActionRoleTemplateInstantiate 按模板创建角色实例。
	ChangeActionRoleTemplateInstantiate ChangeAction = "role_template_instantiate"
	// ChangeActionRoleTemplateSync 把模板基线同步到漂移实例。
	ChangeActionRoleTemplateSync ChangeAction = "role_template_sync"
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
	// ChangeTargetIAMSodGroup 互斥角色组（RBAC2）。
	ChangeTargetIAMSodGroup ChangeTargetType = "iam_sod_group"
	// ChangeTargetIAMRoleTemplate 角色模板基线。
	ChangeTargetIAMRoleTemplate ChangeTargetType = "iam_role_template"
)

// TargetType 返回动作对应的目标类型；未知动作返回 40000。
func (a ChangeAction) TargetType() (ChangeTargetType, error) {
	switch a {
	case ChangeActionRoleCreate, ChangeActionRoleUpdate, ChangeActionRoleDelete,
		ChangeActionRoleClone, ChangeActionRolePermissionReplace,
		ChangeActionRoleConstraintUpdate:
		return ChangeTargetIAMRole, nil
	case ChangeActionBindingCreate, ChangeActionBindingDelete:
		return ChangeTargetIAMBbinding, nil
	case ChangeActionBreakglassRequest:
		return ChangeTargetIAMBreakglass, nil
	case ChangeActionSodGroupCreate, ChangeActionSodGroupUpdate, ChangeActionSodGroupDelete:
		return ChangeTargetIAMSodGroup, nil
	case ChangeActionRoleTemplateCreate, ChangeActionRoleTemplateUpdate,
		ChangeActionRoleTemplateDelete, ChangeActionRoleTemplatePermissionReplace,
		ChangeActionRoleTemplateInstantiate, ChangeActionRoleTemplateSync:
		return ChangeTargetIAMRoleTemplate, nil
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
		ChangeActionBindingCreate, ChangeActionBreakglassRequest,
		ChangeActionSodGroupCreate, ChangeActionRoleTemplateCreate,
		ChangeActionRoleTemplateInstantiate:
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
//   - 端别与商户必须自洽：A 端商户为空，B 端商户非空（租户隔离，SEC-14）；
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
	// MerchantID 商户 ID：A 端恒为空串，B 端必须等于 Principal 的商户（SEC-14）。
	//
	// 该字段只由服务端从 Principal 派生，绝不接受请求体或载荷传入；
	// 它是审批单的租户归属事实，仓储与用例都据此做过滤与二次校验。
	MerchantID string
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
	// ExpiresAt 审批时效截止；nil 表示未设置，由配置在创建时计算。
	ExpiresAt *time.Time
	// PayloadVersion 载荷结构版本。
	PayloadVersion int32
	// RequestedBy 申请人 subject_id。
	RequestedBy string
	// RequestedAt 申请时间（UTC）。
	RequestedAt time.Time
	// ApprovedBy 决定人 subject_id（通过或拒绝都记录）。
	ApprovedBy string
	// ApprovedAt 决定时间（UTC）；未决定为 nil。
	ApprovedAt *time.Time
	// DecisionReason 审批结论说明；拒绝时必填。
	DecisionReason string
	// AppliedBy 执行应用的操作人 subject_id。
	AppliedBy string
	// AppliedAt 实际应用时间（UTC）；未应用为 nil。
	AppliedAt *time.Time
	// Version 乐观锁版本，初始为 1。
	Version int64
}

// NewChangeRequestParams 创建审批单的入参。
type NewChangeRequestParams struct {
	ID      string
	Channel Channel
	// MerchantID 商户 ID；B 端必填，A 端必须为空（由 transport 从 Principal 派生）。
	MerchantID string
	Action     ChangeAction
	TargetID   string
	Payload    []byte
	Reason     string
	// ExpiresAt 审批时效截止；nil 表示不过期（仅测试 / 兼容场景使用）。
	ExpiresAt *time.Time
	Actor     string
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
	if err := validateChangeTenant(p.Channel, p.MerchantID); err != nil {
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
	var expiresAt *time.Time
	if p.ExpiresAt != nil {
		value := p.ExpiresAt.UTC()
		if !value.After(now) {
			return nil, errInvalid(ErrInvalidChangeStatus, "change request expiry must be in the future")
		}
		expiresAt = &value
	}
	return &ChangeRequest{
		ID:             p.ID,
		Channel:        p.Channel,
		MerchantID:     p.MerchantID,
		Action:         p.Action,
		TargetType:     targetType,
		TargetID:       p.TargetID,
		Payload:        payload,
		Status:         ChangeStatusPending,
		Reason:         reason,
		ExpiresAt:      expiresAt,
		PayloadVersion: ChangePayloadVersion,
		RequestedBy:    p.Actor,
		RequestedAt:    now,
		Version:        1,
	}, nil
}

// IsPending 报告审批单是否仍可被决定。
func (c *ChangeRequest) IsPending() bool { return c.Status == ChangeStatusPending }

// VisibleTo 报告审批单是否对给定端别 / 商户可见。
//
// 判定规则与请求解析规则一一对应：A 端只看平台级单据（merchantID 为空），
// B 端只看本商户单据。跨端或跨商户一律返回 false，由调用方统一映射 40400，
// 不得用 40300 区分「存在但无权」，否则等于提供了租户枚举接口（SEC-14）。
func (c *ChangeRequest) VisibleTo(channel Channel, merchantID string) bool {
	if channel != "" && c.Channel != channel {
		return false
	}
	return c.MerchantID == merchantID
}

// IsExpiredAt 报告审批单在 now 时刻是否已超过审批时效。
func (c *ChangeRequest) IsExpiredAt(now time.Time) bool {
	if c.ExpiresAt == nil {
		return false
	}
	return !now.Before(*c.ExpiresAt)
}

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

// Approve 通过审批；默认要求审批人与申请人不同（SoD）。
//
// 超过审批时效的单据必须先由 Expire 收敛为 expired，不能再被通过：
// 审批人看到的载荷可能已与目标聚合格局不一致，迟到的批准等于授权了过期意图。
func (c *ChangeRequest) Approve(approver, decisionReason string, expectedVersion int64) error {
	return c.decide(ChangeStatusApproved, approver, decisionReason, expectedVersion, false)
}

// ApproveAllowingSelf 允许申请人自批（仅供 system_admin 等旁路；须另写 sod_bypass 审计）。
func (c *ChangeRequest) ApproveAllowingSelf(approver, decisionReason string, expectedVersion int64) error {
	return c.decide(ChangeStatusApproved, approver, decisionReason, expectedVersion, true)
}

// Reject 拒绝审批；同样适用职责分离，且必须说明拒绝理由。
func (c *ChangeRequest) Reject(approver, decisionReason string, expectedVersion int64) error {
	return c.decide(ChangeStatusRejected, approver, decisionReason, expectedVersion, false)
}

// RejectAllowingSelf 允许申请人自拒（仅供 system_admin 等旁路；须另写 sod_bypass 审计）。
func (c *ChangeRequest) RejectAllowingSelf(approver, decisionReason string, expectedVersion int64) error {
	return c.decide(ChangeStatusRejected, approver, decisionReason, expectedVersion, true)
}

// decide 收敛通过 / 拒绝的公共校验与状态迁移。
func (c *ChangeRequest) decide(
	status ChangeStatus,
	approver, decisionReason string,
	expectedVersion int64,
	allowSelfApproval bool,
) error {
	if err := c.ensurePending(approver, expectedVersion); err != nil {
		return err
	}
	if status == ChangeStatusApproved && c.IsExpiredAt(nowUTC()) {
		return errConflict(ErrChangeExpired, "change request %s has expired", c.ID)
	}
	if approver == c.RequestedBy && !allowSelfApproval {
		if status == ChangeStatusRejected {
			return errForbidden(ErrSelfApproval,
				"requester %s must not reject their own change request", approver)
		}
		return errForbidden(ErrSelfApproval,
			"requester %s must not approve their own change request (separation of duties)", approver)
	}
	requireReason := status == ChangeStatusRejected
	reason, err := normalizeDecisionReason(decisionReason, requireReason)
	if err != nil {
		return err
	}
	now := nowUTC()
	c.Status = status
	c.ApprovedBy = approver
	c.ApprovedAt = &now
	c.DecisionReason = reason
	c.Version++
	return nil
}

// Expire 把超时的待审批单收敛为 expired。
//
// 只有 pending 且确实超过时效才允许；其它状态返回 40900，避免清理任务误改已决定单据。
func (c *ChangeRequest) Expire(expectedVersion int64) error {
	if c.Status != ChangeStatusPending {
		return errConflict(ErrChangeNotPending,
			"change request %s is %s, not pending", c.ID, string(c.Status))
	}
	if expectedVersion != c.Version {
		return errConflict(ErrVersionConflict,
			"change request %s version %d does not match expected %d", c.ID, c.Version, expectedVersion)
	}
	if !c.IsExpiredAt(nowUTC()) {
		return errConflict(ErrChangeNotPending, "change request %s has not expired yet", c.ID)
	}
	c.Status = ChangeStatusExpired
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

// BindTarget 在应用阶段回填新建聚合的真实 ID。
//
// 新建类动作在申请时还没有业务主键，审批单只携带载荷；应用成功后必须回填
// 目标 ID，否则审计无法从审批单追溯到实际落库的角色 / 绑定。已回填的单子
// 不允许再改，避免同一张单子指向不同目标。
func (c *ChangeRequest) BindTarget(targetID string) error {
	if c.Status != ChangeStatusApproved {
		return errConflict(ErrChangeNotApproved,
			"change request %s is %s; target can only be bound after approval", c.ID, string(c.Status))
	}
	if c.TargetID != "" {
		return errConflict(ErrInvalidChangeTarget,
			"change request %s already targets %s", c.ID, c.TargetID)
	}
	if err := validateChangeTarget(c.Action, targetID); err != nil {
		return err
	}
	if targetID == "" {
		return errInvalid(ErrInvalidChangeTarget, "change target id is required")
	}
	c.TargetID = targetID
	return nil
}

// MarkApplied 标记审批单已应用。
//
// 应用动作必须幂等：重复调用返回 40900，由调用方回读状态，避免同一审批单被
// 应用到两个不同版本的目标聚合。
func (c *ChangeRequest) MarkApplied(actor string, expectedVersion int64) error {
	if c.Status != ChangeStatusApproved {
		return errConflict(ErrChangeNotApproved,
			"change request %s is %s; only approved requests can be applied", c.ID, string(c.Status))
	}
	if expectedVersion != c.Version {
		return errConflict(ErrVersionConflict,
			"change request %s version %d does not match expected %d", c.ID, c.Version, expectedVersion)
	}
	if actor == "" {
		return errInvalid(ErrMissingActor, "change request applier is required")
	}
	now := nowUTC()
	c.Status = ChangeStatusApplied
	c.AppliedBy = actor
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

// validateChangeTenant 校验端别与商户组合，防止出现「无主」审批单。
//
// A 端审批单作用于平台级角色（platform role 的 merchant_id 必须为空），
// 带上商户会让审批单与目标聚合的作用域不自洽；B 端审批单必须锁定商户，
// 否则仓储无法按租户过滤，应用阶段也无法证明目标属于当前商户。
func validateChangeTenant(channel Channel, merchantID string) error {
	if len(merchantID) > MaxChangeMerchantIDLen {
		return errInvalid(ErrInvalidChangeTenant,
			"change request merchant id exceeds %d bytes", MaxChangeMerchantIDLen)
	}
	switch channel {
	case ChannelAdmin:
		if merchantID != "" {
			return errInvalid(ErrInvalidChangeTenant,
				"channel admin change request must not carry merchant id")
		}
	case ChannelBiz:
		if merchantID == "" {
			return errInvalid(ErrInvalidChangeTenant,
				"channel biz change request requires merchant id")
		}
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

// normalizeDecisionReason 去除首尾空白并校验审批结论说明；拒绝时必须填写。
func normalizeDecisionReason(reason string, required bool) (string, error) {
	reason = strings.TrimSpace(reason)
	if required && reason == "" {
		return "", errInvalid(ErrInvalidDecisionReason,
			"decision reason is required when rejecting a change request")
	}
	if len(reason) > MaxDecisionReasonLen {
		return "", errInvalid(ErrInvalidDecisionReason,
			"decision reason exceeds %d bytes", MaxDecisionReasonLen)
	}
	return reason, nil
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
	// B 端不得申请平台级作用域：否则审批人批准一张「本商户」单据，实际创建出
	// 跨租户可见的 platform 角色。这里在申请阶段就拒绝，避免把越权意图带上审批台。
	if channel == ChannelBiz {
		if raw, ok := fields["scope_type"]; ok {
			var scope string
			if err := json.Unmarshal(raw, &scope); err != nil ||
				(scope != "" && scope != string(RoleScopeMerchant)) {
				return nil, errInvalid(ErrInvalidChangeTenant,
					"channel biz change payload may only target merchant scope")
			}
		}
	}
	return slices.Clone(trimmed), nil
}

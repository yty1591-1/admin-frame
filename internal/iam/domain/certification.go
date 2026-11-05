package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

// 访问复核（Access Certification）领域模型。
//
// 设计目标：把一次季度 / 事件驱动的权限复核固化为「活动 + 任务」两级聚合，
// 裁决前再次校验绑定指纹，避免复核期间发生的授权变更被旧任务覆盖（TOCTOU）。

// CertificationStatus 是复核活动生命周期。
type CertificationStatus string

const (
	// CertificationStatusDraft 草稿：可删除、不可裁决。
	CertificationStatusDraft CertificationStatus = "draft"
	// CertificationStatusActive 进行中：可裁决、可自动过期。
	CertificationStatusActive CertificationStatus = "active"
	// CertificationStatusClosed 已关闭：终态，不再产生新任务。
	CertificationStatusClosed CertificationStatus = "closed"
)

// Validate 校验活动状态取值。
func (s CertificationStatus) Validate() error {
	switch s {
	case CertificationStatusDraft, CertificationStatusActive, CertificationStatusClosed:
		return nil
	default:
		return errInvalid(ErrInvalidCertificationStatus, "certification status %q is not supported", string(s))
	}
}

// IsTerminal 报告活动是否已不可回流。
func (s CertificationStatus) IsTerminal() bool { return s == CertificationStatusClosed }

// CertificationTaskStatus 是复核任务处理结果。
type CertificationTaskStatus string

const (
	// CertificationTaskStatusPending 待裁决。
	CertificationTaskStatusPending CertificationTaskStatus = "pending"
	// CertificationTaskStatusConfirmed 确认保留权限。
	CertificationTaskStatusConfirmed CertificationTaskStatus = "confirmed"
	// CertificationTaskStatusRevoked 驳回并回收权限。
	CertificationTaskStatusRevoked CertificationTaskStatus = "revoked"
	// CertificationTaskStatusExpired 到期未裁决（或自动过期）。
	CertificationTaskStatusExpired CertificationTaskStatus = "expired"
	// CertificationTaskStatusSkipped 自动回收前发现绑定已变更，跳过并留痕。
	CertificationTaskStatusSkipped CertificationTaskStatus = "skipped"
)

// Validate 校验任务状态取值。
func (s CertificationTaskStatus) Validate() error {
	switch s {
	case CertificationTaskStatusPending, CertificationTaskStatusConfirmed,
		CertificationTaskStatusRevoked, CertificationTaskStatusExpired,
		CertificationTaskStatusSkipped:
		return nil
	default:
		return errInvalid(ErrInvalidCertificationStatus, "certification task status %q is not supported", string(s))
	}
}

// IsTerminal 报告任务是否已处理完毕。
func (s CertificationTaskStatus) IsTerminal() bool {
	return s != CertificationTaskStatusPending
}

// CertificationDecision 是复核人对任务的裁决动作。
type CertificationDecision string

const (
	// CertificationDecisionConfirm 确认保留绑定。
	CertificationDecisionConfirm CertificationDecision = "confirmed"
	// CertificationDecisionRevoke 驳回并回收绑定。
	CertificationDecisionRevoke CertificationDecision = "revoked"
)

// Validate 校验裁决取值。
func (d CertificationDecision) Validate() error {
	switch d {
	case CertificationDecisionConfirm, CertificationDecisionRevoke:
		return nil
	default:
		return errInvalid(ErrInvalidCertificationTask, "certification decision %q is not supported", string(d))
	}
}

// taskStatusForDecision 把裁决动作映射为任务终态。
func taskStatusForDecision(d CertificationDecision) CertificationTaskStatus {
	if d == CertificationDecisionRevoke {
		return CertificationTaskStatusRevoked
	}
	return CertificationTaskStatusConfirmed
}

// CertificationCampaign 是一次访问复核活动。
//
// RoleIDs 为空表示复核该租户内全部角色；PrivilegedOnly 为 true 时只复核
// 含高危权限（iam:* / data:export / config:*）的角色，二者可叠加。
type CertificationCampaign struct {
	// ID 雪花 ID。
	ID string
	// Name 活动名称。
	Name string
	// Channel 端别。
	Channel Channel
	// MerchantID B 端商户 ID；A 端为空串。
	MerchantID string
	// RoleIDs 复核角色范围；空表示全部。
	RoleIDs []string
	// PrivilegedOnly 只复核含高危权限的角色。
	PrivilegedOnly bool
	// Status 生命周期状态。
	Status CertificationStatus
	// DueAt 裁决截止时间（UTC）。
	DueAt time.Time
	// CreatedBy 创建人 subject_id。
	CreatedBy string
	// CreatedAt 创建时间（UTC）。
	CreatedAt time.Time
	// ClosedAt 关闭时间（UTC）；未关闭为 nil。
	ClosedAt *time.Time
	// Version 乐观锁版本，初始为 1。
	Version int64
	// TaskCount 活动下任务总数（生成清单后同步）。
	TaskCount int
}

// NewCertificationCampaignParams 创建活动的入参。
type NewCertificationCampaignParams struct {
	ID             string
	Name           string
	Channel        Channel
	MerchantID     string
	RoleIDs        []string
	PrivilegedOnly bool
	DueAt          time.Time
	CreatedBy      string
}

// NewCertificationCampaign 构造并校验活动。
func NewCertificationCampaign(p NewCertificationCampaignParams) (*CertificationCampaign, error) {
	name := strings.TrimSpace(p.Name)
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrInvalidCertificationCampaign, "certification campaign id is required")
	}
	if name == "" {
		return nil, errInvalid(ErrInvalidCertificationCampaign, "certification campaign name is required")
	}
	if len(name) > 128 {
		return nil, errInvalid(ErrInvalidCertificationCampaign, "certification campaign name exceeds 128 bytes")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if err := validateCertificationTenant(p.Channel, strings.TrimSpace(p.MerchantID)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.CreatedBy) == "" {
		return nil, errInvalid(ErrMissingActor, "certification campaign creator is required")
	}
	now := nowUTC()
	dueAt := p.DueAt.UTC()
	if dueAt.IsZero() || !dueAt.After(now) {
		return nil, errInvalid(ErrInvalidCertificationCampaign, "certification due_at must be in the future")
	}
	roleIDs, err := normalizeCertificationRoleIDs(p.RoleIDs)
	if err != nil {
		return nil, err
	}
	return &CertificationCampaign{
		ID:             p.ID,
		Name:           name,
		Channel:        p.Channel,
		MerchantID:     strings.TrimSpace(p.MerchantID),
		RoleIDs:        roleIDs,
		PrivilegedOnly: p.PrivilegedOnly,
		Status:         CertificationStatusDraft,
		DueAt:          dueAt,
		CreatedBy:      strings.TrimSpace(p.CreatedBy),
		CreatedAt:      now,
		Version:        1,
	}, nil
}

// Activate 把草稿置为进行中。
//
// 重复激活幂等成功；已关闭活动不可重新激活，避免关闭后的裁决语义被推翻。
func (c *CertificationCampaign) Activate(_ time.Time) error {
	if err := c.Status.Validate(); err != nil {
		return err
	}
	switch c.Status {
	case CertificationStatusActive:
		return nil
	case CertificationStatusClosed:
		return errConflict(ErrCertificationNotActive, "certification campaign %s is closed", c.ID)
	default:
		c.Status = CertificationStatusActive
		c.Version++
		return nil
	}
}

// Close 关闭活动。
//
// 关闭是幂等终态迁移：已关闭时保持首次关闭事实，不更新时间与版本。
func (c *CertificationCampaign) Close(now time.Time, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return errInvalid(ErrMissingActor, "certification close actor is required")
	}
	if err := c.Status.Validate(); err != nil {
		return err
	}
	if c.Status == CertificationStatusClosed {
		return nil
	}
	value := now.UTC()
	c.Status = CertificationStatusClosed
	c.ClosedAt = &value
	c.Version++
	return nil
}

// IsOverdue 报告是否已过裁决截止时间（含等号）。
func (c *CertificationCampaign) IsOverdue(now time.Time) bool {
	return !now.UTC().Before(c.DueAt)
}

// AcceptsDecisions 报告当前是否允许人工裁决：进行中且未过期。
func (c *CertificationCampaign) AcceptsDecisions(now time.Time) bool {
	return c.Status == CertificationStatusActive && !c.IsOverdue(now)
}

// CertificationTask 是活动内针对「主体 + 角色 + 绑定」的一条复核任务。
//
// BindingFingerprint 在生成清单时计算；裁决驳回前重新计算并比对，
// 防止复核期间解绑 / 重绑 / 改期后被旧任务误回收（TOCTOU）。
type CertificationTask struct {
	ID                 string
	CampaignID         string
	SubjectType        SubjectType
	SubjectID          string
	MerchantID         string
	RoleID             string
	BindingID          string
	BindingFingerprint string
	ReviewerID         string
	Status             CertificationTaskStatus
	Decision           CertificationDecision
	Reason             string
	DecidedBy          string
	DecidedAt          *time.Time
	CreatedAt          time.Time
	Version            int64
}

// NewCertificationTaskParams 创建任务入参。
type NewCertificationTaskParams struct {
	ID                 string
	CampaignID         string
	SubjectType        SubjectType
	SubjectID          string
	MerchantID         string
	RoleID             string
	BindingID          string
	BindingFingerprint string
	ReviewerID         string
}

// NewCertificationTask 构造并校验任务。
func NewCertificationTask(p NewCertificationTaskParams) (*CertificationTask, error) {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.CampaignID) == "" {
		return nil, errInvalid(ErrInvalidCertificationTask, "certification task id and campaign id are required")
	}
	if err := p.SubjectType.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.SubjectID) == "" || strings.TrimSpace(p.RoleID) == "" {
		return nil, errInvalid(ErrInvalidCertificationTask, "certification task subject and role are required")
	}
	if strings.TrimSpace(p.BindingID) == "" {
		return nil, errInvalid(ErrInvalidCertificationTask, "certification task binding id is required")
	}
	if strings.TrimSpace(p.ReviewerID) == "" {
		return nil, errInvalid(ErrInvalidCertificationTask, "certification task reviewer is required")
	}
	if strings.TrimSpace(p.BindingFingerprint) == "" {
		return nil, errInvalid(ErrInvalidCertificationTask, "certification task binding fingerprint is required")
	}
	if err := validateCertificationTenantForSubject(p.SubjectType, strings.TrimSpace(p.MerchantID)); err != nil {
		return nil, err
	}
	return &CertificationTask{
		ID:                 p.ID,
		CampaignID:         p.CampaignID,
		SubjectType:        p.SubjectType,
		SubjectID:          strings.TrimSpace(p.SubjectID),
		MerchantID:         strings.TrimSpace(p.MerchantID),
		RoleID:             strings.TrimSpace(p.RoleID),
		BindingID:          strings.TrimSpace(p.BindingID),
		BindingFingerprint: p.BindingFingerprint,
		ReviewerID:         strings.TrimSpace(p.ReviewerID),
		Status:             CertificationTaskStatusPending,
		CreatedAt:          nowUTC(),
		Version:            1,
	}, nil
}

// Decide 记录人工裁决。
//
// 仅 pending 可裁决；重复提交相同裁决幂等成功，相反裁决返回冲突而不是覆盖。
func (t *CertificationTask) Decide(decision CertificationDecision, actor, reason string, now time.Time) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(actor) == "" {
		return errInvalid(ErrMissingActor, "certification decision actor is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errInvalid(ErrInvalidDecisionReason, "certification decision reason is required")
	}
	if len(reason) > 512 {
		return errInvalid(ErrInvalidDecisionReason, "certification decision reason exceeds 512 bytes")
	}
	switch t.Status {
	case CertificationTaskStatusConfirmed, CertificationTaskStatusRevoked:
		if t.Decision == decision {
			return nil
		}
		return errConflict(ErrCertificationNotPending,
			"certification task %s is already decided as %s", t.ID, string(t.Decision))
	case CertificationTaskStatusPending:
	default:
		return errConflict(ErrCertificationNotPending,
			"certification task %s is %s and can no longer be decided", t.ID, string(t.Status))
	}
	decidedAt := now.UTC()
	t.Status = taskStatusForDecision(decision)
	t.Decision = decision
	t.Reason = reason
	t.DecidedBy = strings.TrimSpace(actor)
	t.DecidedAt = &decidedAt
	t.Version++
	return nil
}

// Expire 把待裁决任务标记为到期。
//
// 只用于自动过期扫描；已过期幂等成功，其他终态不得改写。
func (t *CertificationTask) Expire(now time.Time) error {
	switch t.Status {
	case CertificationTaskStatusExpired:
		return nil
	case CertificationTaskStatusPending:
	default:
		return errConflict(ErrCertificationNotPending,
			"certification task %s is %s and can no longer expire", t.ID, string(t.Status))
	}
	expiredAt := now.UTC()
	t.Status = CertificationTaskStatusExpired
	t.DecidedAt = &expiredAt
	t.Version++
	return nil
}

// MarkSkipped 记录自动回收前的安全跳过。
//
// 与 Expire 不同，skipped 明确表示「发现了并发变更，因此没有执行回收」，
// 供证据导出区分「到期未处理」与「因变更被拦截」。
func (t *CertificationTask) MarkSkipped(actor, reason string, now time.Time) error {
	if strings.TrimSpace(actor) == "" {
		return errInvalid(ErrMissingActor, "certification skip actor is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errInvalid(ErrInvalidDecisionReason, "certification skip reason is required")
	}
	switch t.Status {
	case CertificationTaskStatusSkipped:
		return nil
	case CertificationTaskStatusPending:
	default:
		return errConflict(ErrCertificationNotPending,
			"certification task %s is %s and can no longer be skipped", t.ID, string(t.Status))
	}
	skippedAt := now.UTC()
	t.Status = CertificationTaskStatusSkipped
	t.Reason = reason
	t.DecidedBy = strings.TrimSpace(actor)
	t.DecidedAt = &skippedAt
	t.Version++
	return nil
}

// BindingFingerprint 由绑定关键字段派生，用于裁决前二次校验。
//
// 只包含影响「这条授权是否仍是生成任务时的那一条」的字段：
// 主体、端别、商户、角色、有效期与撤销状态。授权人 / 创建时间不参与，
// 因为这些字段变化不代表授权关系本身发生变化。
func BindingFingerprint(b *Binding) string {
	if b == nil {
		return ""
	}
	expiresAt := "-"
	if b.ExpiresAt != nil {
		expiresAt = b.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	revoked := "0"
	if b.IsRevoked() {
		revoked = "1"
	}
	raw := strings.Join([]string{
		string(b.Channel), string(b.SubjectType), b.SubjectID, b.MerchantID,
		b.RoleID, expiresAt, revoked,
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// validateCertificationTenant 校验活动的端别与商户组合。
func validateCertificationTenant(channel Channel, merchantID string) error {
	switch channel {
	case ChannelBiz:
		if merchantID == "" {
			return errInvalid(ErrInvalidCertificationTenant, "channel biz requires merchant id")
		}
	case ChannelAdmin:
		if merchantID != "" {
			return errInvalid(ErrInvalidCertificationTenant, "channel admin must not carry merchant id")
		}
	default:
		return errInvalid(ErrInvalidChannel, "channel %q must be admin or biz", string(channel))
	}
	return nil
}

// validateCertificationTenantForSubject 校验任务商户与主体类型匹配。
func validateCertificationTenantForSubject(subjectType SubjectType, merchantID string) error {
	switch subjectType {
	case SubjectMerchantStaff:
		if merchantID == "" {
			return errInvalid(ErrInvalidCertificationTask, "merchant staff task requires merchant id")
		}
	case SubjectStaff:
		if merchantID != "" {
			return errInvalid(ErrInvalidCertificationTask, "staff task must not carry merchant id")
		}
	}
	return nil
}

// normalizeCertificationRoleIDs 去空白、去重并保持输入顺序。
func normalizeCertificationRoleIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errInvalid(ErrInvalidCertificationCampaign, "certification role id must not be empty")
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return slices.Clip(out), nil
}

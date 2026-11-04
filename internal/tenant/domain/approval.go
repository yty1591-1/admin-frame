package domain

import (
	"fmt"
	"strings"
	"time"
)

// ApprovalAction 是入驻审批事件的动作。
//
// 审批采用 append-only 事件序列（每次提交 / 通过 / 驳回各一条），而不是在
// 单行上改状态：驳回后允许重新提交，单行会把「上次为什么被驳回」覆盖掉，
// 「审核留痕」就只剩一句口头承诺。当前审批态由事件序列派生（见 DeriveApproval）。
type ApprovalAction string

// 审批动作取值。
const (
	// ApprovalActionSubmit 提交入驻申请（创建租户后、激活前）。
	ApprovalActionSubmit ApprovalAction = "submit"
	// ApprovalActionApprove 审核通过：是激活的前置条件之一。
	ApprovalActionApprove ApprovalAction = "approve"
	// ApprovalActionReject 审核驳回：必须写原因，允许修改后重新提交。
	ApprovalActionReject ApprovalAction = "reject"
)

// 审批字段长度上限，与迁移 000023 的列宽对齐。
const (
	// MaxApprovalNoteLength 审批意见长度上限（tenant_approval.note VARCHAR(255)）。
	MaxApprovalNoteLength = 255
	// MaxApprovalActorLength 操作者主体 ID 上限（tenant_approval.actor_id VARCHAR(64)）。
	MaxApprovalActorLength = 64
	// maxApprovalHistory 派生当前态时回看的事件条数上限。
	//
	// 只回看最近的窗口而不是全量历史：审批事件随「驳回 → 重提」增长，
	// 但当前态只取决于最新一轮；窗口足够覆盖最坏情况（每轮 3 条事件）。
	maxApprovalHistory = 200
)

// Valid 判断动作是否为已定义取值。
func (a ApprovalAction) Valid() bool {
	switch a {
	case ApprovalActionSubmit, ApprovalActionApprove, ApprovalActionReject:
		return true
	default:
		return false
	}
}

// Decided 判断该动作是否是一次裁决（通过 / 驳回）。
func (a ApprovalAction) Decided() bool {
	return a == ApprovalActionApprove || a == ApprovalActionReject
}

// Approval 是一条审批事件。
type Approval struct {
	// ID 自增序号；仓储写入后回填，用于稳定排序。
	ID int64
	// TenantID 所属租户（隔离边界）。
	TenantID string
	// Action 事件动作。
	Action ApprovalAction
	// ActorID 操作者主体 ID；提交人与审批人不得为同一人。
	ActorID string
	// Note 申请说明或审批意见；驳回时必填。
	Note string
	// CreatedAt 发生时间（UTC）。
	CreatedAt time.Time
}

// NewApproval 构造并校验一条审批事件。
func NewApproval(tenantID string, action ApprovalAction, actorID, note string, now time.Time) (Approval, error) {
	event := Approval{
		TenantID:  strings.TrimSpace(tenantID),
		Action:    action,
		ActorID:   strings.TrimSpace(actorID),
		Note:      strings.TrimSpace(note),
		CreatedAt: now.UTC(),
	}
	if err := event.Validate(); err != nil {
		return Approval{}, err
	}
	return event, nil
}

// Validate 校验事件自身完整性（仓储写入前的最后一道）。
func (a Approval) Validate() error {
	if strings.TrimSpace(a.TenantID) == "" {
		return ErrApprovalTenantRequired
	}
	if !a.Action.Valid() {
		return fmt.Errorf("%w: %q", ErrApprovalActionInvalid, string(a.Action))
	}
	if strings.TrimSpace(a.ActorID) == "" {
		return ErrApprovalActorRequired
	}
	if len(a.ActorID) > MaxApprovalActorLength {
		return ErrApprovalActorTooLong
	}
	if len(a.Note) > MaxApprovalNoteLength {
		return ErrApprovalNoteTooLong
	}
	if a.Action == ApprovalActionReject && a.Note == "" {
		return ErrApprovalRejectNoteRequired
	}
	if a.CreatedAt.IsZero() {
		return ErrApprovalTimeRequired
	}
	return nil
}

// ApprovalStatus 是审批的派生状态。
type ApprovalStatus string

// 审批派生状态取值。
const (
	// ApprovalStatusDraft 租户已创建但尚未提交申请。
	ApprovalStatusDraft ApprovalStatus = "draft"
	// ApprovalStatusSubmitted 已提交，等待裁决。
	ApprovalStatusSubmitted ApprovalStatus = "submitted"
	// ApprovalStatusApproved 最近一次裁决为通过。
	ApprovalStatusApproved ApprovalStatus = "approved"
	// ApprovalStatusRejected 最近一次裁决为驳回。
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

// Valid 判断派生状态是否为已定义取值。
func (s ApprovalStatus) Valid() bool {
	switch s {
	case ApprovalStatusDraft, ApprovalStatusSubmitted, ApprovalStatusApproved, ApprovalStatusRejected:
		return true
	default:
		return false
	}
}

// ApprovalDecision 是审批事件序列的派生视图。
type ApprovalDecision struct {
	// Status 当前审批状态。
	Status ApprovalStatus
	// Latest 最新一条事件；draft 时为零值。
	Latest Approval
	// Submitter 当前这一轮提交的申请人；用于职责分离校验。
	Submitter string
	// History 回看到的事件（时间正序），供详情接口展示。
	History []Approval
}

// Approved 报告当前是否处于「已通过」状态。
func (d ApprovalDecision) Approved() bool {
	return d.Status == ApprovalStatusApproved
}

// Pending 报告当前是否处于「待裁决」状态。
func (d ApprovalDecision) Pending() bool {
	return d.Status == ApprovalStatusSubmitted
}

// DeriveApproval 由事件序列派生当前审批态。
//
// 入参按时间正序；空序列即 draft。派生规则只有一条：取最后一条事件——
// 提交 → submitted，通过 → approved，驳回 → rejected；Submitter 取最近一次
// submit 的操作者（重提会更新它）。
func DeriveApproval(events []Approval) ApprovalDecision {
	decision := ApprovalDecision{Status: ApprovalStatusDraft}
	if len(events) == 0 {
		return decision
	}
	// 只保留窗口内的事件，避免异常长的历史影响派生结果。
	if len(events) > maxApprovalHistory {
		events = events[len(events)-maxApprovalHistory:]
	}
	decision.History = events
	latest := events[len(events)-1]
	decision.Latest = latest
	switch latest.Action {
	case ApprovalActionSubmit:
		decision.Status = ApprovalStatusSubmitted
	case ApprovalActionApprove:
		decision.Status = ApprovalStatusApproved
	case ApprovalActionReject:
		decision.Status = ApprovalStatusRejected
	}
	// Submitter 从后往前找最近一次 submit：重提后申请人可能换了人。
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Action == ApprovalActionSubmit {
			decision.Submitter = events[i].ActorID
			break
		}
	}
	return decision
}

// EnsureSubmittable 校验当前是否允许（重新）提交申请。
func (d ApprovalDecision) EnsureSubmittable() error {
	switch d.Status {
	case ApprovalStatusDraft, ApprovalStatusRejected:
		return nil
	case ApprovalStatusSubmitted:
		return ErrApprovalAlreadySubmitted
	case ApprovalStatusApproved:
		return ErrApprovalAlreadyApproved
	default:
		return fmt.Errorf("%w: %q", ErrApprovalStatusInvalid, string(d.Status))
	}
}

// EnsureDecidable 校验是否允许对当前申请作出裁决。
func (d ApprovalDecision) EnsureDecidable() error {
	if !d.Pending() {
		return ErrApprovalNotPending
	}
	return nil
}

// EnsureNotSelfReview 校验职责分离：审批人不得是当前申请的提交人。
func (d ApprovalDecision) EnsureNotSelfReview(actorID string) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrApprovalActorRequired
	}
	if d.Submitter != "" && strings.TrimSpace(actorID) == d.Submitter {
		return ErrApprovalSelfReview
	}
	return nil
}

// MaxApprovalHistory 返回派生窗口大小（供仓储按需截断）。
func MaxApprovalHistory() int {
	return maxApprovalHistory
}

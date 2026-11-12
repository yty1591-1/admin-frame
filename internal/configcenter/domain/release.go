package domain

import (
	"fmt"
	"strings"
	"time"
)

// ReleaseStatus 是发布单状态。
type ReleaseStatus string

// 发布单状态枚举。
const (
	ReleaseStatusDraft         ReleaseStatus = "draft"
	ReleaseStatusPendingReview ReleaseStatus = "pending_review"
	ReleaseStatusApproved      ReleaseStatus = "approved"
	ReleaseStatusActive        ReleaseStatus = "active"
	ReleaseStatusSuperseded    ReleaseStatus = "superseded"
	ReleaseStatusRolledBack    ReleaseStatus = "rolled_back"
	ReleaseStatusRejected      ReleaseStatus = "rejected"
	ReleaseStatusCancelled     ReleaseStatus = "cancelled"
)

// Valid 判断发布状态枚举合法性。
func (s ReleaseStatus) Valid() bool {
	switch s {
	case ReleaseStatusDraft, ReleaseStatusPendingReview, ReleaseStatusApproved,
		ReleaseStatusActive, ReleaseStatusSuperseded, ReleaseStatusRolledBack,
		ReleaseStatusRejected, ReleaseStatusCancelled:
		return true
	default:
		return false
	}
}

// releaseTransitions 是发布单状态迁移矩阵。
//
// active 只能通过 pointer CAS 切换为 superseded / rolled_back；
// approved 是审批通过但尚未生效，生效动作单独留痕。
var releaseTransitions = map[ReleaseStatus][]ReleaseStatus{
	ReleaseStatusDraft:         {ReleaseStatusPendingReview, ReleaseStatusCancelled},
	ReleaseStatusPendingReview: {ReleaseStatusApproved, ReleaseStatusRejected, ReleaseStatusCancelled},
	ReleaseStatusApproved:      {ReleaseStatusActive, ReleaseStatusCancelled},
	ReleaseStatusActive:        {ReleaseStatusSuperseded, ReleaseStatusRolledBack},
	ReleaseStatusSuperseded:    {},
	ReleaseStatusRolledBack:    {},
	ReleaseStatusRejected:      {},
	ReleaseStatusCancelled:     {},
}

// CanTransitionTo 判断发布状态迁移是否合法。
func (s ReleaseStatus) CanTransitionTo(target ReleaseStatus) bool {
	for _, allowed := range releaseTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// ReleaseStrategy 是灰度策略。
type ReleaseStrategy string

// 灰度策略枚举。
const (
	StrategyAll          ReleaseStrategy = "all"
	StrategyPercentage   ReleaseStrategy = "percentage"
	StrategyTenantList   ReleaseStrategy = "tenant_list"
	StrategyChannelList  ReleaseStrategy = "channel_list"
	StrategyInstanceList ReleaseStrategy = "instance_list"
)

// Valid 判断灰度策略枚举合法性。
func (s ReleaseStrategy) Valid() bool {
	switch s {
	case StrategyAll, StrategyPercentage, StrategyTenantList, StrategyChannelList, StrategyInstanceList:
		return true
	default:
		return false
	}
}

// Release 是不可变发布快照头；内容由 ReleaseItem 固定。
type Release struct {
	ID                int64
	Env               string
	Namespace         string
	ReleaseNo         int64
	Name              string
	Description       string
	Status            ReleaseStatus
	Strategy          ReleaseStrategy
	TargetRuleJSON    string
	SnapshotHash      string
	SnapshotSize      int
	SnapshotSignature string
	SnapshotKeyID     string
	BasedOnReleaseID  *int64
	CreatedBy         string
	SubmittedBy       string
	ApprovedBy        string
	ApprovedAt        *time.Time
	PublishedAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Validate 校验发布单的不变量。
func (r Release) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("configcenter: release id is required")
	}
	if !ValidEnv(r.Env) {
		return fmt.Errorf("configcenter: invalid env %q", r.Env)
	}
	if !ValidNamespace(r.Namespace) {
		return fmt.Errorf("configcenter: invalid namespace %q", r.Namespace)
	}
	if r.ReleaseNo < 1 {
		return fmt.Errorf("configcenter: release_no must be >= 1")
	}
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > MaxReleaseName {
		return fmt.Errorf("configcenter: name is required and must not exceed %d bytes", MaxReleaseName)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("configcenter: invalid release status %q", r.Status)
	}
	if !r.Strategy.Valid() {
		return fmt.Errorf("configcenter: invalid strategy %q", r.Strategy)
	}
	if err := validateJSON(r.TargetRuleJSON); err != nil {
		return fmt.Errorf("configcenter: target_rule_json: %w", err)
	}
	if r.Strategy != StrategyAll && strings.TrimSpace(r.TargetRuleJSON) == "" {
		return fmt.Errorf("configcenter: target_rule_json is required for strategy %q", r.Strategy)
	}
	if !ValidSHA256(r.SnapshotHash) {
		return fmt.Errorf("configcenter: snapshot_hash must be 64 lowercase hex characters")
	}
	if r.SnapshotSize < 0 {
		return fmt.Errorf("configcenter: snapshot_size must be >= 0")
	}
	if strings.TrimSpace(r.CreatedBy) == "" {
		return fmt.Errorf("configcenter: created_by is required")
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return fmt.Errorf("configcenter: created_at and updated_at are required")
	}
	return nil
}

// ReleaseItem 是发布快照中的一条键版本映射。
type ReleaseItem struct {
	ReleaseID   int64
	ItemID      int64
	ItemVersion int64
	ConfigKey   string
	ValueHash   string
}

// Validate 校验快照条目的不变量。
func (i ReleaseItem) Validate() error {
	if i.ReleaseID <= 0 || i.ItemID <= 0 {
		return fmt.Errorf("configcenter: release_id and item_id are required")
	}
	if i.ItemVersion < 1 {
		return fmt.Errorf("configcenter: item_version must be >= 1")
	}
	if !ValidKey(i.ConfigKey) {
		return fmt.Errorf("configcenter: invalid config_key %q", i.ConfigKey)
	}
	if !ValidSHA256(i.ValueHash) {
		return fmt.Errorf("configcenter: value_hash must be 64 lowercase hex characters")
	}
	return nil
}

// ReviewDecision 是审批结论。
type ReviewDecision string

// 审批结论枚举。
const (
	DecisionApproved ReviewDecision = "approved"
	DecisionRejected ReviewDecision = "rejected"
)

// Valid 判断审批结论合法性。
func (d ReviewDecision) Valid() bool {
	return d == DecisionApproved || d == DecisionRejected
}

// ReleaseReview 是一条审批记录；提交人不得自审由应用层保证。
type ReleaseReview struct {
	ID        int64
	ReleaseID int64
	Reviewer  string
	Decision  ReviewDecision
	Comment   string
	CreatedAt time.Time
}

// Validate 校验审批记录的不变量。
func (r ReleaseReview) Validate() error {
	if r.ID <= 0 || r.ReleaseID <= 0 {
		return fmt.Errorf("configcenter: review id and release_id are required")
	}
	if strings.TrimSpace(r.Reviewer) == "" {
		return fmt.Errorf("configcenter: reviewer is required")
	}
	if !r.Decision.Valid() {
		return fmt.Errorf("configcenter: invalid review decision %q", r.Decision)
	}
	if len(r.Comment) > MaxReviewComment {
		return fmt.Errorf("configcenter: comment exceeds %d bytes", MaxReviewComment)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("configcenter: created_at is required")
	}
	return nil
}

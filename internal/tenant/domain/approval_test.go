package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// approvalAt 构造一条事件（测试用固定时间）。
func approvalAt(t *testing.T, action domain.ApprovalAction, actor, note string, at time.Time) domain.Approval {
	t.Helper()
	event, err := domain.NewApproval("t-1", action, actor, note, at)
	if err != nil {
		t.Fatalf("NewApproval(%s) error = %v", action, err)
	}
	return event
}

var approvalBase = time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)

// TestDeriveApprovalStates 覆盖事件序列到派生状态的映射。
func TestDeriveApprovalStates(t *testing.T) {
	cases := []struct {
		name   string
		events []domain.Approval
		want   domain.ApprovalStatus
	}{
		{"无事件 = draft", nil, domain.ApprovalStatusDraft},
		{"submit = submitted", []domain.Approval{
			approvalAt(t, domain.ApprovalActionSubmit, "u1", "申请", approvalBase),
		}, domain.ApprovalStatusSubmitted},
		{"submit+approve = approved", []domain.Approval{
			approvalAt(t, domain.ApprovalActionSubmit, "u1", "申请", approvalBase),
			approvalAt(t, domain.ApprovalActionApprove, "u2", "通过", approvalBase.Add(time.Minute)),
		}, domain.ApprovalStatusApproved},
		{"submit+reject = rejected", []domain.Approval{
			approvalAt(t, domain.ApprovalActionSubmit, "u1", "申请", approvalBase),
			approvalAt(t, domain.ApprovalActionReject, "u2", "材料不全", approvalBase.Add(time.Minute)),
		}, domain.ApprovalStatusRejected},
		{"驳回后重提 = submitted", []domain.Approval{
			approvalAt(t, domain.ApprovalActionSubmit, "u1", "申请", approvalBase),
			approvalAt(t, domain.ApprovalActionReject, "u2", "材料不全", approvalBase.Add(time.Minute)),
			approvalAt(t, domain.ApprovalActionSubmit, "u1", "补齐材料", approvalBase.Add(2*time.Minute)),
		}, domain.ApprovalStatusSubmitted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := domain.DeriveApproval(tc.events)
			if decision.Status != tc.want {
				t.Fatalf("Status = %s, want %s", decision.Status, tc.want)
			}
		})
	}
}

// TestDeriveApprovalTracksLatestSubmitter 重提后申请人必须跟着更新，
// 否则职责分离会拿旧提交人去比对，出现「换了人提交、旧人却被禁审批」。
func TestDeriveApprovalTracksLatestSubmitter(t *testing.T) {
	events := []domain.Approval{
		approvalAt(t, domain.ApprovalActionSubmit, "alice", "申请", approvalBase),
		approvalAt(t, domain.ApprovalActionReject, "bob", "材料不全", approvalBase.Add(time.Minute)),
		approvalAt(t, domain.ApprovalActionSubmit, "carol", "重提", approvalBase.Add(2*time.Minute)),
	}
	decision := domain.DeriveApproval(events)
	if decision.Submitter != "carol" {
		t.Fatalf("Submitter = %q, want carol", decision.Submitter)
	}
	if len(decision.History) != 3 {
		t.Fatalf("History 长度 = %d, want 3（驳回记录必须保留）", len(decision.History))
	}
}

// TestApprovalDecisionGuards 覆盖提交 / 裁决的前置条件。
func TestApprovalDecisionGuards(t *testing.T) {
	draft := domain.DeriveApproval(nil)
	if err := draft.EnsureSubmittable(); err != nil {
		t.Fatalf("draft 应可提交, error = %v", err)
	}
	if err := draft.EnsureDecidable(); !errors.Is(err, domain.ErrApprovalNotPending) {
		t.Fatalf("draft 不可裁决, error = %v", err)
	}

	submitted := domain.DeriveApproval([]domain.Approval{
		approvalAt(t, domain.ApprovalActionSubmit, "alice", "", approvalBase),
	})
	if err := submitted.EnsureSubmittable(); !errors.Is(err, domain.ErrApprovalAlreadySubmitted) {
		t.Fatalf("已提交不可重复提交, error = %v", err)
	}
	if err := submitted.EnsureDecidable(); err != nil {
		t.Fatalf("submitted 应可裁决, error = %v", err)
	}
	if err := submitted.EnsureNotSelfReview("alice"); !errors.Is(err, domain.ErrApprovalSelfReview) {
		t.Fatalf("提交人自审必须被拒, error = %v", err)
	}
	if err := submitted.EnsureNotSelfReview("bob"); err != nil {
		t.Fatalf("他人审批应允许, error = %v", err)
	}

	approved := domain.DeriveApproval([]domain.Approval{
		approvalAt(t, domain.ApprovalActionSubmit, "alice", "", approvalBase),
		approvalAt(t, domain.ApprovalActionApprove, "bob", "", approvalBase.Add(time.Minute)),
	})
	if !approved.Approved() {
		t.Fatal("approved 事件序列的 Approved() 必须为 true")
	}
	if err := approved.EnsureSubmittable(); !errors.Is(err, domain.ErrApprovalAlreadyApproved) {
		t.Fatalf("已通过不可再提交, error = %v", err)
	}
	if err := approved.EnsureDecidable(); !errors.Is(err, domain.ErrApprovalNotPending) {
		t.Fatalf("已通过不可再裁决, error = %v", err)
	}
}

// TestNewApprovalValidation 校验事件自身的完整性。
func TestNewApprovalValidation(t *testing.T) {
	cases := []struct {
		name   string
		action domain.ApprovalAction
		actor  string
		note   string
		want   error
	}{
		{"驳回必须写原因", domain.ApprovalActionReject, "bob", "  ", domain.ErrApprovalRejectNoteRequired},
		{"操作者必填", domain.ApprovalActionSubmit, " ", "申请", domain.ErrApprovalActorRequired},
		{"操作者超长", domain.ApprovalActionSubmit, strings.Repeat("a", domain.MaxApprovalActorLength+1), "", domain.ErrApprovalActorTooLong},
		{"意见超长", domain.ApprovalActionApprove, "bob", strings.Repeat("好", domain.MaxApprovalNoteLength+1), domain.ErrApprovalNoteTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewApproval("t-1", tc.action, tc.actor, tc.note, approvalBase)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := domain.NewApproval("  ", domain.ApprovalActionSubmit, "alice", "", approvalBase); !errors.Is(err, domain.ErrApprovalTenantRequired) {
		t.Fatalf("租户 ID 必填, error = %v", err)
	}
	if _, err := domain.NewApproval("t-1", domain.ApprovalAction("escalate"), "alice", "", approvalBase); !errors.Is(err, domain.ErrApprovalActionInvalid) {
		t.Fatalf("非法动作必须被拒, error = %v", err)
	}
	if _, err := domain.NewApproval("t-1", domain.ApprovalActionSubmit, "alice", "", time.Time{}); !errors.Is(err, domain.ErrApprovalTimeRequired) {
		t.Fatalf("时间必填, error = %v", err)
	}
}

// TestApprovalActionHelpers 覆盖动作语义辅助方法。
func TestApprovalActionHelpers(t *testing.T) {
	for _, action := range []domain.ApprovalAction{
		domain.ApprovalActionSubmit, domain.ApprovalActionApprove, domain.ApprovalActionReject,
	} {
		if !action.Valid() {
			t.Fatalf("%s 应为合法动作", action)
		}
	}
	if domain.ApprovalAction("").Valid() {
		t.Fatal("空动作不得合法")
	}
	if !domain.ApprovalActionApprove.Decided() || !domain.ApprovalActionReject.Decided() {
		t.Fatal("approve / reject 是裁决动作")
	}
	if domain.ApprovalActionSubmit.Decided() {
		t.Fatal("submit 不是裁决动作")
	}
}

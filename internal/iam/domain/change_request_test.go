package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mustNewChangeRequest 构造一张合法的 A 端审批单，可用 mutate 覆盖字段。
func mustNewChangeRequest(t *testing.T, mutate func(*domain.NewChangeRequestParams)) *domain.ChangeRequest {
	t.Helper()
	params := domain.NewChangeRequestParams{
		ID:       "change-1",
		Channel:  domain.ChannelAdmin,
		Action:   domain.ChangeActionRolePermissionReplace,
		TargetID: "role-1",
		Payload:  []byte(`{"permissions":["iam:role:read"]}`),
		Reason:   "授予只读权限",
		Actor:    "requester-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	request, err := domain.NewChangeRequest(params)
	if err != nil {
		t.Fatalf("NewChangeRequest: %v", err)
	}
	return request
}

func TestNewChangeRequestDefaults(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if request.Status != domain.ChangeStatusPending {
		t.Fatalf("status=%s", request.Status)
	}
	if request.Version != 1 {
		t.Fatalf("version=%d", request.Version)
	}
	if request.TargetType != domain.ChangeTargetIAMRole {
		t.Fatalf("target type=%s", request.TargetType)
	}
	if request.RequestedAt.IsZero() {
		t.Fatal("requested_at must be set")
	}
	if request.ApprovedAt != nil || request.AppliedAt != nil {
		t.Fatal("decision timestamps must be nil before review")
	}
	if !request.IsPending() || request.IsApproved() {
		t.Fatal("new request must be pending")
	}
}

func TestNewChangeRequestNormalizesPayloadAndReason(t *testing.T) {
	request := mustNewChangeRequest(t, func(p *domain.NewChangeRequestParams) {
		p.Payload = nil
		p.Reason = "  授权  "
	})
	if string(request.Payload) != "{}" {
		t.Fatalf("payload=%s", request.Payload)
	}
	if request.Reason != "授权" {
		t.Fatalf("reason=%q", request.Reason)
	}
}

func TestNewChangeRequestAllowsEmptyTargetForCreateActions(t *testing.T) {
	request := mustNewChangeRequest(t, func(p *domain.NewChangeRequestParams) {
		p.Action = domain.ChangeActionRoleCreate
		p.TargetID = ""
	})
	if request.TargetID != "" || request.TargetType != domain.ChangeTargetIAMRole {
		t.Fatalf("target=%q/%q", request.TargetType, request.TargetID)
	}
}

func TestNewChangeRequestValidation(t *testing.T) {
	oversizedPayload := []byte(`{"data":"` + strings.Repeat("a", domain.MaxChangePayloadBytes) + `"}`)

	cases := []struct {
		name     string
		mutate   func(*domain.NewChangeRequestParams)
		sentinel error
	}{
		{"empty id", func(p *domain.NewChangeRequestParams) { p.ID = "" }, domain.ErrEmptyInput},
		{"missing actor", func(p *domain.NewChangeRequestParams) { p.Actor = "" }, domain.ErrMissingActor},
		{"bad channel", func(p *domain.NewChangeRequestParams) { p.Channel = "c" }, domain.ErrInvalidChannel},
		{"unknown action", func(p *domain.NewChangeRequestParams) { p.Action = "role_wipe" }, domain.ErrInvalidChangeAction},
		{"missing target", func(p *domain.NewChangeRequestParams) { p.TargetID = "" }, domain.ErrInvalidChangeTarget},
		{"target too long", func(p *domain.NewChangeRequestParams) {
			p.TargetID = strings.Repeat("x", domain.MaxChangeTargetIDLen+1)
		}, domain.ErrInvalidChangeTarget},
		{"payload too large", func(p *domain.NewChangeRequestParams) {
			p.Payload = oversizedPayload
		}, domain.ErrChangePayloadInvalid},
		{"payload invalid json", func(p *domain.NewChangeRequestParams) {
			p.Payload = []byte(`{"a":`)
		}, domain.ErrChangePayloadInvalid},
		{"payload not object", func(p *domain.NewChangeRequestParams) {
			p.Payload = []byte(`["iam:role:read"]`)
		}, domain.ErrChangePayloadInvalid},
		{"payload sensitive key", func(p *domain.NewChangeRequestParams) {
			p.Payload = []byte(`{"access_token":"abc"}`)
		}, domain.ErrChangePayloadInvalid},
		{"reason too long", func(p *domain.NewChangeRequestParams) {
			p.Reason = strings.Repeat("r", domain.MaxChangeReasonLen+1)
		}, domain.ErrEmptyInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := domain.NewChangeRequestParams{
				ID:       "change-1",
				Channel:  domain.ChannelAdmin,
				Action:   domain.ChangeActionRoleUpdate,
				TargetID: "role-1",
				Actor:    "requester-1",
			}
			tc.mutate(&params)
			_, err := domain.NewChangeRequest(params)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err=%v, want sentinel %v", err, tc.sentinel)
			}
			if !errors.Is(err, apperr.ErrBadRequest) {
				t.Fatalf("err=%v, want bad request", err)
			}
		})
	}
}

func TestNewChangeRequestRejectsBizTenantOverride(t *testing.T) {
	_, err := domain.NewChangeRequest(domain.NewChangeRequestParams{
		ID:         "change-1",
		Channel:    domain.ChannelBiz,
		MerchantID: "m-1",
		Action:     domain.ChangeActionRoleCreate,
		Payload:    []byte(`{"merchant_id":"m-2","code":"owner"}`),
		Actor:      "requester-1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, domain.ErrChangeTenantOverride) {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("err=%v, want forbidden", err)
	}
}

func TestNewChangeRequestRejectsBizPlatformScopePayload(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{name: "platform scope", payload: `{"scope_type":"platform","code":"owner"}`},
		{name: "invalid scope type", payload: `{"scope_type":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewChangeRequest(domain.NewChangeRequestParams{
				ID:         "change-1",
				Channel:    domain.ChannelBiz,
				MerchantID: "m-1",
				Action:     domain.ChangeActionRoleCreate,
				Payload:    []byte(tc.payload),
				Actor:      "requester-1",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, domain.ErrInvalidChangeTenant) {
				t.Fatalf("err=%v, want ErrInvalidChangeTenant", err)
			}
			if !errors.Is(err, apperr.ErrBadRequest) {
				t.Fatalf("err=%v, want bad request", err)
			}
		})
	}
}

// 商户作用域（或未声明 scope_type）的载荷仍然允许，避免过度收紧误伤正常申请。
func TestNewChangeRequestAcceptsBizMerchantScopePayload(t *testing.T) {
	for _, payload := range []string{
		`{"scope_type":"merchant","code":"owner"}`,
		`{"scope_type":"","code":"owner"}`,
		`{"code":"owner"}`,
	} {
		_, err := domain.NewChangeRequest(domain.NewChangeRequestParams{
			ID:         "change-1",
			Channel:    domain.ChannelBiz,
			MerchantID: "m-1",
			Action:     domain.ChangeActionRoleCreate,
			Payload:    []byte(payload),
			Actor:      "requester-1",
		})
		if err != nil {
			t.Fatalf("payload %s: unexpected error %v", payload, err)
		}
	}
}

func TestApproveEnforcesSeparationOfDuties(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	err := request.Approve("requester-1", "", 1)
	if err == nil {
		t.Fatal("expected self-approval rejection")
	}
	if !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("err=%v, want forbidden", err)
	}
	if !request.IsPending() || request.Version != 1 {
		t.Fatalf("state must not change: status=%s version=%d", request.Status, request.Version)
	}
}

func TestApproveHappyPathAndIdempotency(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if err := request.Approve("approver-1", "", 1); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !request.IsApproved() || request.Version != 2 {
		t.Fatalf("status=%s version=%d", request.Status, request.Version)
	}
	if request.ApprovedBy != "approver-1" || request.ApprovedAt == nil {
		t.Fatalf("approved by/at=%q/%v", request.ApprovedBy, request.ApprovedAt)
	}
	if err := request.Approve("approver-2", "", 2); err == nil {
		t.Fatal("expected double approval to fail")
	} else if !errors.Is(err, domain.ErrChangeNotPending) {
		t.Fatalf("err=%v", err)
	}
}

func TestApproveRejectsVersionConflict(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	err := request.Approve("approver-1", "", 9)
	if !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("err=%v", err)
	}
	if !request.IsPending() {
		t.Fatal("status must stay pending")
	}
}

func TestRejectFlow(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if err := request.Reject("approver-1", "rejected in test", 1); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if request.Status != domain.ChangeStatusRejected || !request.Status.IsTerminal() {
		t.Fatalf("status=%s", request.Status)
	}
	if request.ApprovedAt == nil {
		t.Fatal("rejection must record decision time")
	}
	if err := request.Approve("approver-2", "", 2); !errors.Is(err, domain.ErrChangeNotPending) {
		t.Fatalf("err=%v", err)
	}
}

func TestCancelOnlyByRequester(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if err := request.Cancel("someone-else", 1); !errors.Is(err, domain.ErrNotRequester) {
		t.Fatalf("err=%v", err)
	}
	if err := request.Cancel("requester-1", 1); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if request.Status != domain.ChangeStatusCancelled || request.Version != 2 {
		t.Fatalf("status=%s version=%d", request.Status, request.Version)
	}
	if err := request.Cancel("requester-1", 2); !errors.Is(err, domain.ErrChangeNotPending) {
		t.Fatalf("err=%v", err)
	}
}

func TestMarkAppliedRequiresApproval(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if err := request.MarkApplied("applier-1", 1); !errors.Is(err, domain.ErrChangeNotApproved) {
		t.Fatalf("err=%v", err)
	}
	if err := request.Approve("approver-1", "", 1); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := request.MarkApplied("applier-1", 9); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("err=%v", err)
	}
	if err := request.MarkApplied("applier-1", 2); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	if request.Status != domain.ChangeStatusApplied || request.AppliedAt == nil {
		t.Fatalf("status=%s applied_at=%v", request.Status, request.AppliedAt)
	}
	if err := request.MarkApplied("applier-1", 3); !errors.Is(err, domain.ErrChangeNotApproved) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodePayload(t *testing.T) {
	request := mustNewChangeRequest(t, func(p *domain.NewChangeRequestParams) {
		p.Payload = []byte(`{"permissions":["iam:role:read","iam:role:write"]}`)
	})
	var payload struct {
		Permissions []string `json:"permissions"`
	}
	if err := request.DecodePayload(&payload); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if len(payload.Permissions) != 2 {
		t.Fatalf("permissions=%v", payload.Permissions)
	}
	if err := request.DecodePayload(nil); !errors.Is(err, domain.ErrEmptyInput) {
		t.Fatalf("err=%v", err)
	}

	request.Payload = []byte(`not-json`)
	if err := request.DecodePayload(&payload); !errors.Is(err, domain.ErrChangePayloadInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestChangeStatusValidate(t *testing.T) {
	for _, status := range []domain.ChangeStatus{
		domain.ChangeStatusPending,
		domain.ChangeStatusApproved,
		domain.ChangeStatusRejected,
		domain.ChangeStatusApplied,
		domain.ChangeStatusCancelled,
	} {
		if err := status.Validate(); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
	}
	if err := domain.ChangeStatus("done").Validate(); !errors.Is(err, domain.ErrInvalidChangeStatus) {
		t.Fatalf("err=%v", err)
	}
	if domain.ChangeStatusPending.IsTerminal() || domain.ChangeStatusApproved.IsTerminal() {
		t.Fatal("pending / approved must not be terminal")
	}
}

func TestChangeActionTargetTypes(t *testing.T) {
	cases := map[domain.ChangeAction]domain.ChangeTargetType{
		domain.ChangeActionRoleCreate:            domain.ChangeTargetIAMRole,
		domain.ChangeActionRoleClone:             domain.ChangeTargetIAMRole,
		domain.ChangeActionBindingCreate:         domain.ChangeTargetIAMBbinding,
		domain.ChangeActionBindingDelete:         domain.ChangeTargetIAMBbinding,
		domain.ChangeActionBreakglassRequest:     domain.ChangeTargetIAMBreakglass,
		domain.ChangeActionRolePermissionReplace: domain.ChangeTargetIAMRole,
	}
	for action, want := range cases {
		got, err := action.TargetType()
		if err != nil {
			t.Fatalf("action %s: %v", action, err)
		}
		if got != want {
			t.Fatalf("action %s target=%s, want %s", action, got, want)
		}
	}
}

func TestDecisionsRequireActor(t *testing.T) {
	for name, decide := range map[string]func(*domain.ChangeRequest) error{
		"approve": func(r *domain.ChangeRequest) error { return r.Approve("", "", 1) },
		"reject":  func(r *domain.ChangeRequest) error { return r.Reject("", "rejected in test", 1) },
		"cancel":  func(r *domain.ChangeRequest) error { return r.Cancel("", 1) },
	} {
		request := mustNewChangeRequest(t, nil)
		if err := decide(request); !errors.Is(err, domain.ErrMissingActor) {
			t.Fatalf("%s err=%v", name, err)
		}
	}
}

func TestRejectEnforcesSeparationOfDuties(t *testing.T) {
	request := mustNewChangeRequest(t, nil)
	if err := request.Reject("requester-1", "rejected in test", 1); !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("err=%v", err)
	}
	if !request.IsPending() {
		t.Fatal("status must stay pending")
	}
}

func TestNewChangeRequestTrimsWhitespacePayload(t *testing.T) {
	request := mustNewChangeRequest(t, func(p *domain.NewChangeRequestParams) {
		p.Payload = []byte("  {\n \"code\": \"owner\"\n}  ")
	})
	if string(request.Payload) != "{\n \"code\": \"owner\"\n}" {
		t.Fatalf("payload=%q", request.Payload)
	}
}

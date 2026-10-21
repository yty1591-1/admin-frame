package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
)

// mustNewBinding 构造 A 端员工绑定；可用 mutate 覆盖字段。
func mustNewBinding(t *testing.T, mutate func(*NewBindingParams)) *Binding {
	t.Helper()
	params := NewBindingParams{
		ID:          "binding-1",
		Channel:     ChannelAdmin,
		SubjectType: SubjectStaff,
		SubjectID:   "u-1",
		RoleID:      "role-1",
		GrantedBy:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	binding, err := NewBinding(params)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	return binding
}

func TestNewBindingValidation(t *testing.T) {
	future := time.Now().UTC().Add(time.Minute)
	past := time.Now().UTC().Add(-time.Minute)

	cases := []struct {
		name    string
		mutate  func(*NewBindingParams)
		wantErr error
	}{
		{name: "missing id", mutate: func(p *NewBindingParams) { p.ID = "" }, wantErr: ErrInvalidBinding},
		{name: "missing subject", mutate: func(p *NewBindingParams) { p.SubjectID = "" }, wantErr: ErrInvalidBinding},
		{name: "missing role", mutate: func(p *NewBindingParams) { p.RoleID = "" }, wantErr: ErrInvalidBinding},
		{name: "missing granter", mutate: func(p *NewBindingParams) { p.GrantedBy = "" }, wantErr: ErrMissingActor},
		{name: "bad channel", mutate: func(p *NewBindingParams) { p.Channel = "c" }, wantErr: ErrInvalidChannel},
		{name: "bad subject type", mutate: func(p *NewBindingParams) { p.SubjectType = "guest" }, wantErr: ErrInvalidSubjectType},
		{
			name:    "admin with merchant staff",
			mutate:  func(p *NewBindingParams) { p.SubjectType = SubjectMerchantStaff },
			wantErr: ErrInvalidSubjectType,
		},
		{name: "admin with merchant id", mutate: func(p *NewBindingParams) { p.MerchantID = "m-1" }, wantErr: ErrInvalidBinding},
		{
			name: "biz with staff",
			mutate: func(p *NewBindingParams) {
				p.Channel = ChannelBiz
				p.MerchantID = "m-1"
			},
			wantErr: ErrInvalidSubjectType,
		},
		{
			name: "biz without merchant",
			mutate: func(p *NewBindingParams) {
				p.Channel = ChannelBiz
				p.SubjectType = SubjectMerchantStaff
			},
			wantErr: ErrInvalidBinding,
		},
		{
			name:    "expiry in the past",
			mutate:  func(p *NewBindingParams) { p.ExpiresAt = &past },
			wantErr: ErrInvalidBinding,
		},
		{
			name:   "expiry in the future",
			mutate: func(p *NewBindingParams) { p.ExpiresAt = &future },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := NewBindingParams{
				ID:          "binding-1",
				Channel:     ChannelAdmin,
				SubjectType: SubjectStaff,
				SubjectID:   "u-1",
				RoleID:      "role-1",
				GrantedBy:   "actor-1",
			}
			tc.mutate(&params)
			binding, err := NewBinding(params)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if binding.CreatedAt.IsZero() {
					t.Fatal("created_at must be set")
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestNewBizBinding(t *testing.T) {
	binding := mustNewBinding(t, func(p *NewBindingParams) {
		p.Channel = ChannelBiz
		p.SubjectType = SubjectMerchantStaff
		p.MerchantID = "m-1"
	})
	if binding.MerchantID != "m-1" {
		t.Fatalf("merchant=%q", binding.MerchantID)
	}
}

func TestBindingExpiry(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	binding := mustNewBinding(t, func(p *NewBindingParams) { p.ExpiresAt = &expires })

	if binding.IsExpired(now) {
		t.Fatal("binding must be effective before expiry")
	}
	if !binding.IsEffective(now) {
		t.Fatal("IsEffective must mirror IsExpired")
	}
	if !binding.IsExpired(expires) {
		t.Fatal("binding must be expired at the exact expiry instant")
	}
	if !binding.IsExpired(expires.Add(time.Second)) {
		t.Fatal("binding must stay expired after expiry")
	}
}

// TestExpiredBindingIgnored 是 TESTING_STANDARDS 必测函数。
func TestExpiredBindingIgnored(t *testing.T) {
	now := time.Now().UTC()
	binding := mustNewBinding(t, nil)
	if binding.IsExpired(now) {
		t.Fatal("binding without expiry must never expire")
	}

	expires := now.Add(time.Minute)
	limited := mustNewBinding(t, func(p *NewBindingParams) { p.ExpiresAt = &expires })
	if !limited.IsExpired(expires.Add(time.Nanosecond)) {
		t.Fatal("expired binding must be filtered by IsExpired")
	}
}

// TestCrossChannelIsolation 是 TESTING_STANDARDS 必测函数：跨端绑定必须拒绝。
func TestCrossChannelIsolation(t *testing.T) {
	bizRole := mustNewBizRole(t, nil)
	adminBinding := mustNewBinding(t, nil)
	if err := adminBinding.ValidateAgainst(bizRole, time.Now().UTC()); !errors.Is(err, ErrBindingChannelMismatch) {
		t.Fatalf("err=%v, want ErrBindingChannelMismatch", err)
	}

	adminRole := mustNewRole(t, nil)
	bizBinding := mustNewBinding(t, func(p *NewBindingParams) {
		p.Channel = ChannelBiz
		p.SubjectType = SubjectMerchantStaff
		p.MerchantID = "m-1"
	})
	if err := bizBinding.ValidateAgainst(adminRole, time.Now().UTC()); !errors.Is(err, ErrBindingChannelMismatch) {
		t.Fatalf("err=%v, want ErrBindingChannelMismatch", err)
	}
}

func TestBindingValidateAgainstMerchantMismatch(t *testing.T) {
	role := mustNewBizRole(t, nil)
	other := mustNewBinding(t, func(p *NewBindingParams) {
		p.Channel = ChannelBiz
		p.SubjectType = SubjectMerchantStaff
		p.MerchantID = "m-2"
	})
	if err := other.ValidateAgainst(role, time.Now().UTC()); !errors.Is(err, ErrBindingChannelMismatch) {
		t.Fatalf("err=%v, want ErrBindingChannelMismatch", err)
	}
}

func TestBindingValidateAgainstRoleState(t *testing.T) {
	binding := mustNewBinding(t, nil)
	now := time.Now().UTC()

	if err := binding.ValidateAgainst(nil, now); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("err=%v, want ErrInvalidBinding", err)
	}

	deleted := mustNewRole(t, nil)
	if err := deleted.Delete("actor-1", deleted.Version); err != nil {
		t.Fatal(err)
	}
	if err := binding.ValidateAgainst(deleted, now); !errors.Is(err, ErrRoleDeleted) {
		t.Fatalf("err=%v, want ErrRoleDeleted", err)
	}

	disabled := mustNewRole(t, nil)
	if err := disabled.Disable("actor-1", disabled.Version); err != nil {
		t.Fatal(err)
	}
	if err := binding.ValidateAgainst(disabled, now); !errors.Is(err, ErrRoleNotActive) {
		t.Fatalf("err=%v, want ErrRoleNotActive", err)
	}

	active := mustNewRole(t, nil)
	active.ID = "role-2"
	if err := binding.ValidateAgainst(active, now); err != nil {
		t.Fatalf("active role must accept binding: %v", err)
	}
}

func TestBreakglassBindingRequiresBoundedExpiry(t *testing.T) {
	now := time.Now().UTC()
	role := &Role{
		ID:      "role-bg",
		Channel: ChannelAdmin,
		Scope:   RoleScopePlatform,
		Code:    "breakglass_admin",
		Type:    RoleTypeBreakglass,
		Status:  RoleStatusActive,
		Version: 1,
	}

	noExpiry := mustNewBinding(t, nil)
	if err := noExpiry.ValidateAgainst(role, now); !errors.Is(err, ErrBreakglassRequiresExpiry) {
		t.Fatalf("err=%v, want ErrBreakglassRequiresExpiry", err)
	}

	tooLong := now.Add(MaxBreakglassDuration + time.Minute)
	longBinding := mustNewBinding(t, func(p *NewBindingParams) { p.ExpiresAt = &tooLong })
	if err := longBinding.ValidateAgainst(role, now); !errors.Is(err, ErrBreakglassRequiresExpiry) {
		t.Fatalf("err=%v, want ErrBreakglassRequiresExpiry", err)
	}

	expired := now.Add(-time.Minute)
	expiredBinding := &Binding{Channel: ChannelAdmin, ExpiresAt: &expired}
	if err := expiredBinding.ValidateAgainst(role, now); !errors.Is(err, ErrBreakglassRequiresExpiry) {
		t.Fatalf("err=%v, want ErrBreakglassRequiresExpiry", err)
	}

	within := now.Add(MaxBreakglassDuration / 2)
	ok := mustNewBinding(t, func(p *NewBindingParams) { p.ExpiresAt = &within })
	if err := ok.ValidateAgainst(role, now); err != nil {
		t.Fatalf("bounded breakglass binding must be accepted: %v", err)
	}
}

func TestSubjectQueryEffectiveTime(t *testing.T) {
	if got := (SubjectQuery{}).EffectiveTime(); got.IsZero() {
		t.Fatal("zero At must fall back to now")
	}
	at := time.Date(2026, 10, 21, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if got := (SubjectQuery{At: at}).EffectiveTime(); !got.Equal(at.UTC()) {
		t.Fatalf("got=%v, want %v", got, at.UTC())
	}
}

func TestSubjectSnapshotLookup(t *testing.T) {
	orderScope := DataScope{Resource: "order", Type: DataScopeAll}
	snapshot := &SubjectSnapshot{
		Permissions: []iam.PermissionCode{iam.PermOrderRead, iam.PermFileRead},
		Scopes:      []DataScope{orderScope},
	}
	if !snapshot.HasPermission(iam.PermOrderRead) {
		t.Fatal("HasPermission must find granted code")
	}
	if snapshot.HasPermission(iam.PermOrderWrite) {
		t.Fatal("HasPermission must not invent permissions")
	}
	scope, ok := snapshot.ScopeFor("order")
	if !ok || scope.Type != DataScopeAll {
		t.Fatalf("scope=%+v ok=%v", scope, ok)
	}
	if _, ok := snapshot.ScopeFor("file"); ok {
		t.Fatal("missing resource must not match")
	}

	wildcard := &SubjectSnapshot{Scopes: []DataScope{{Resource: ResourceAll, Type: DataScopeAll}}}
	if scope, ok := wildcard.ScopeFor("file"); !ok || scope.Resource != ResourceAll {
		t.Fatalf("wildcard scope=%+v ok=%v", scope, ok)
	}
}

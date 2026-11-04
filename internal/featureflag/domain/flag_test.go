package domain

import (
	"errors"
	"testing"
	"time"
)

// nowFixed 是测试固定时钟。
var nowFixed = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// TestTransitionMatrixSnapshot 锁定治理状态迁移白名单：
// enabled → disabled | expired；disabled → enabled；expired 为治理终态
// （恢复必须显式编辑到期配置，防止到期治理被一键复活绕过）。
// 改矩阵必须同步更新本快照并通过评审（对齐 TN-03 门禁模式）。
func TestTransitionMatrixSnapshot(t *testing.T) {
	want := map[FlagStatus][]FlagStatus{
		StatusEnabled:  {StatusDisabled, StatusExpired},
		StatusDisabled: {StatusEnabled},
		StatusExpired:  {},
	}
	got := Transitions()
	if len(got) != len(want) {
		t.Fatalf("matrix size = %d, want %d", len(got), len(want))
	}
	for from, targets := range want {
		actual, ok := got[from]
		if !ok || len(actual) != len(targets) {
			t.Fatalf("row %q mismatch: %v", from, actual)
		}
		for i, target := range targets {
			if actual[i] != target {
				t.Fatalf("row %q[%d] = %q, want %q", from, i, actual[i], target)
			}
		}
	}
}

// TestFlagTransition 覆盖合法迁移与非法迁移（含 expired 终态）。
func TestFlagTransition(t *testing.T) {
	flag := Flag{Key: "t.f", Status: StatusEnabled, Permanent: true, Version: 1}

	disabled, err := flag.Transition(StatusDisabled, nowFixed)
	if err != nil || disabled.Status != StatusDisabled {
		t.Fatalf("enable→disabled failed: %v", err)
	}
	if _, err := disabled.Transition(StatusExpired, nowFixed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("disabled→expired must be invalid, got %v", err)
	}
	expired, err := flag.Transition(StatusExpired, nowFixed)
	if err != nil {
		t.Fatalf("enable→expired failed: %v", err)
	}
	if _, err := expired.Transition(StatusEnabled, nowFixed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expired→enabled must be invalid (governance terminal), got %v", err)
	}
}

// TestNewFlagValidation 覆盖构造校验：key 正则 / 长度 / permanent 与
// expires_at 二选一 / 到期必须在将来 / created_by 必填。
func TestNewFlagValidation(t *testing.T) {
	future := nowFixed.Add(24 * time.Hour)
	cases := []struct {
		name      string
		key       string
		permanent bool
		expiresAt time.Time
		createdBy string
		wantErr   error
	}{
		{"ok permanent", "tenant.invitation_email", true, time.Time{}, "u1", nil},
		{"ok expiring", "tenant.invitation_email", false, future, "u1", nil},
		{"empty key", "", true, time.Time{}, "u1", ErrKeyRequired},
		{"bad key", "BadKey", true, time.Time{}, "u1", ErrKeyInvalid},
		{"single segment key", "flag", true, time.Time{}, "u1", ErrKeyInvalid},
		{"no expiry", "t.f", false, time.Time{}, "u1", ErrExpiryRequired},
		{"both", "t.f", true, future, "u1", ErrPermanentWithExpiry},
		{"past expiry", "t.f", false, nowFixed.Add(-time.Hour), "u1", ErrExpiryInPast},
		{"no creator", "t.f", true, time.Time{}, "", ErrCreatedByRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewFlag(tc.key, "desc", nil, tc.permanent, tc.expiresAt, tc.createdBy, nowFixed)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestFlagRulesRoundTrip 序列化往返 + 反序列化时再校验（防脏数据）。
func TestFlagRulesRoundTrip(t *testing.T) {
	rules := []Rule{
		{Channel: ChannelBiz},
		{TenantIDs: []string{"t-1", "t-2"}},
		{Percentage: 5000},
	}
	flag := Flag{Key: "t.f", Status: StatusEnabled, Permanent: true, Version: 1, Rules: rules}
	raw, err := flag.MarshalRules()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Flag
	if err := back.UnmarshalRules(raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Rules) != 3 || back.Rules[2].Percentage != 5000 {
		t.Fatalf("round trip mismatch: %+v", back.Rules)
	}
	if back.Rules[0].Kind() != "channel" || back.Rules[1].Kind() != "tenant" || back.Rules[2].Kind() != "percentage" {
		t.Fatalf("kind mismatch: %s / %s / %s", back.Rules[0].Kind(), back.Rules[1].Kind(), back.Rules[2].Kind())
	}
	if err := back.UnmarshalRules([]byte(`[{"channel":"weird"}]`)); err == nil {
		t.Fatal("dirty rules must be rejected on unmarshal")
	}
}

// TestRuleMatches 覆盖三维度匹配与互斥校验。
func TestRuleMatches(t *testing.T) {
	channel := Rule{Channel: ChannelBiz}
	if ok, _ := channel.Matches(ChannelBiz, "t-1"); !ok {
		t.Fatal("channel rule must match own channel")
	}
	if ok, _ := channel.Matches(ChannelAdmin, "t-1"); ok {
		t.Fatal("channel rule must not match other channel")
	}
	tenant := Rule{TenantIDs: []string{"t-1"}}
	if ok, _ := tenant.Matches(ChannelBiz, "t-1"); !ok {
		t.Fatal("tenant rule must match listed tenant")
	}
	if ok, _ := tenant.Matches(ChannelBiz, "t-9"); ok {
		t.Fatal("tenant rule must not match other tenant")
	}
	multi := Rule{Channel: ChannelBiz, Percentage: 100}
	if err := multi.Validate(); err == nil {
		t.Fatal("multi-dimension rule must be rejected")
	}
	bad := Rule{Percentage: MaxPercentage + 1}
	if err := bad.Validate(); err == nil {
		t.Fatal("percentage beyond max must be rejected")
	}
}

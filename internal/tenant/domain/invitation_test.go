package domain_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestInvitationStatusValid 实现状态机的取值域与终态判定。
func TestInvitationStatusValid(t *testing.T) {
	cases := []struct {
		status   domain.InvitationStatus
		valid    bool
		terminal bool
	}{
		{domain.InvitationPending, true, false},
		{domain.InvitationAccepted, true, true},
		{domain.InvitationRevoked, true, true},
		{domain.InvitationExpired, true, true},
		{"", false, true},          // 未定义取值：不合法，且不可接受
		{"cancelled", false, true}, // 历史枚举不得被当成 pending 放行
	}
	for _, tc := range cases {
		if got := tc.status.Valid(); got != tc.valid {
			t.Fatalf("InvitationStatus(%q).Valid() = %v, want %v", tc.status, got, tc.valid)
		}
		if got := tc.status.Terminal(); got != tc.terminal {
			t.Fatalf("InvitationStatus(%q).Terminal() = %v, want %v", tc.status, got, tc.terminal)
		}
	}
}

// TestNewInviteToken 明文可解码、摘要与明文对应、两次生成互不相同。
func TestNewInviteToken(t *testing.T) {
	plaintext, hash, err := domain.NewInviteToken()
	if err != nil {
		t.Fatalf("NewInviteToken() error = %v", err)
	}
	if plaintext == "" || hash == "" {
		t.Fatal("NewInviteToken() must return both plaintext and hash")
	}
	// URL-safe 且无填充：可直接放进邀请链接。
	if strings.ContainsAny(plaintext, "+/=") {
		t.Fatalf("plaintext must be raw url-safe base64, got %q", plaintext)
	}
	raw, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil {
		t.Fatalf("decode plaintext: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("token bytes = %d, want 32 (256 bit entropy)", len(raw))
	}
	if got := domain.HashInviteToken(plaintext); got != hash {
		t.Fatalf("hash mismatch: %q vs %q", got, hash)
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(hash))
	}

	other, otherHash, err := domain.NewInviteToken()
	if err != nil {
		t.Fatalf("NewInviteToken() second call error = %v", err)
	}
	if other == plaintext || otherHash == hash {
		t.Fatal("two invitations must not share token or hash")
	}
}

// TestHashInviteTokenIsStable 摘要必须确定：重发 / 校验都靠它定位记录。
func TestHashInviteTokenIsStable(t *testing.T) {
	const plaintext = "invite-token-fixture"
	first := domain.HashInviteToken(plaintext)
	if first != domain.HashInviteToken(plaintext) {
		t.Fatal("HashInviteToken must be deterministic")
	}
	if len(first) != 64 || strings.ToLower(first) != first {
		t.Fatalf("HashInviteToken() = %q, want 64 lowercase hex chars", first)
	}
	if domain.HashInviteToken("other") == first {
		t.Fatal("different tokens must not share a hash")
	}
}

// TestNewInvitation 构造：默认 TTL、邮箱归一化、初始 pending。
func TestNewInvitation(t *testing.T) {
	inv, err := domain.NewInvitation(" inv-1 ", " t-1 ", "  User@Example.COM  ", domain.MemberRoleAdmin,
		" admin-1 ", " hash ", 0, fixedNow)
	if err != nil {
		t.Fatalf("NewInvitation() error = %v", err)
	}
	if inv.ID != "inv-1" || inv.TenantID != "t-1" || inv.InvitedBy != "admin-1" || inv.TokenHash != "hash" {
		t.Fatalf("NewInvitation() must trim inputs: %+v", inv)
	}
	if inv.Email != "User@Example.COM" {
		t.Fatalf("NewInvitation() email = %q, want trimmed but case-preserved", inv.Email)
	}
	if inv.Status != domain.InvitationPending {
		t.Fatalf("NewInvitation() status = %q, want pending", inv.Status)
	}
	if got := inv.ExpiresAt; !got.Equal(fixedNow.Add(domain.DefaultInvitationTTL)) {
		t.Fatalf("NewInvitation() expires_at = %v, want default TTL", got)
	}
	if !inv.CreatedAt.Equal(fixedNow) || !inv.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("NewInvitation() timestamps = %v / %v", inv.CreatedAt, inv.UpdatedAt)
	}
	if inv.AcceptedAt != nil || inv.AcceptedBy != "" {
		t.Fatal("NewInvitation() must start unaccepted")
	}

	custom, err := domain.NewInvitation("inv-2", "t-1", "a@b.cn", domain.MemberRoleMember, "", "hash", time.Hour, fixedNow)
	if err != nil {
		t.Fatalf("NewInvitation(custom ttl) error = %v", err)
	}
	if !custom.ExpiresAt.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("custom TTL ignored: %v", custom.ExpiresAt)
	}

	cases := []struct {
		name     string
		id       string
		tenant   string
		email    string
		role     domain.MemberRole
		hash     string
		wantSent error
	}{
		{"empty id", " ", "t-1", "a@b.cn", domain.MemberRoleMember, "h", nil},
		{"empty tenant", "inv", " ", "a@b.cn", domain.MemberRoleMember, "h", nil},
		{"bad email", "inv", "t-1", "not-an-email", domain.MemberRoleMember, "h", nil},
		{"invalid role", "inv", "t-1", "a@b.cn", "root", "h", nil},
		{"empty hash", "inv", "t-1", "a@b.cn", domain.MemberRoleMember, " ", nil},
		{"empty email", "inv", "t-1", "", domain.MemberRoleMember, "h", domain.ErrInvitationEmailRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewInvitation(tc.id, tc.tenant, tc.email, tc.role, "", tc.hash, time.Hour, fixedNow)
			if err == nil {
				t.Fatal("NewInvitation() must fail")
			}
			if tc.wantSent != nil && !errors.Is(err, tc.wantSent) {
				t.Fatalf("NewInvitation() error = %v, want %v", err, tc.wantSent)
			}
		})
	}
}

// TestNormalizeInvitationEmail 邮箱口径：去空白、限长、必须含 @。
func TestNormalizeInvitationEmail(t *testing.T) {
	got, err := domain.NormalizeInvitationEmail("  a@b.cn ")
	if err != nil || got != "a@b.cn" {
		t.Fatalf("NormalizeInvitationEmail() = %q, %v", got, err)
	}

	if _, err := domain.NormalizeInvitationEmail("   "); !errors.Is(err, domain.ErrInvitationEmailRequired) {
		t.Fatalf("empty email error = %v, want ErrInvitationEmailRequired", err)
	}
	tooLong := strings.Repeat("a", domain.MaxInvitationEmailLength) + "@b.cn"
	if _, err := domain.NormalizeInvitationEmail(tooLong); !errors.Is(err, domain.ErrInvitationEmailTooLong) {
		t.Fatalf("too long email error = %v, want ErrInvitationEmailTooLong", err)
	}
	// 内部空白字符（含制表 / 换行）与缺失 @ 都必须拒绝：
	// 这类地址不可能投递成功，放过去只会在通知中心变成死信。
	for _, bad := range []string{"a@b c", "a@b\n.cn", "a\t@b.cn", "ab.cn"} {
		var emailErr *domain.InvalidEmailError
		if _, err := domain.NormalizeInvitationEmail(bad); !errors.As(err, &emailErr) {
			t.Fatalf("email %q must be rejected with InvalidEmailError, got %v", bad, err)
		}
	}
}

// invitationFixture 构造一条可接受的邀请，供后续用例复用。
func invitationFixture(t *testing.T) domain.Invitation {
	t.Helper()
	inv, err := domain.NewInvitation("inv-1", "t-1", "user@example.com", domain.MemberRoleMember,
		"admin-1", "hash", time.Hour, fixedNow)
	if err != nil {
		t.Fatalf("NewInvitation() error = %v", err)
	}
	return inv
}

// TestInvitationValidate 回读校验：必填、枚举、已接受必须有接受者。
func TestInvitationValidate(t *testing.T) {
	valid := invitationFixture(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	acceptedAt := fixedNow
	accepted := valid
	accepted.Status = domain.InvitationAccepted
	accepted.AcceptedBy = "u-1"
	accepted.AcceptedAt = &acceptedAt
	if err := accepted.Validate(); err != nil {
		t.Fatalf("Validate(accepted) error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(domain.Invitation) domain.Invitation
	}{
		{"missing id", func(i domain.Invitation) domain.Invitation { i.ID = " "; return i }},
		{"missing tenant", func(i domain.Invitation) domain.Invitation { i.TenantID = ""; return i }},
		{"bad email", func(i domain.Invitation) domain.Invitation { i.Email = "nope"; return i }},
		{"invalid role", func(i domain.Invitation) domain.Invitation { i.Role = "root"; return i }},
		{"missing hash", func(i domain.Invitation) domain.Invitation { i.TokenHash = " "; return i }},
		{"invalid status", func(i domain.Invitation) domain.Invitation { i.Status = "cancelled"; return i }},
		{"accepted without acceptee", func(i domain.Invitation) domain.Invitation {
			i.Status = domain.InvitationAccepted
			return i
		}},
		{"accepted without accept time", func(i domain.Invitation) domain.Invitation {
			i.Status = domain.InvitationAccepted
			i.AcceptedBy = "u-1"
			return i
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(valid).Validate(); err == nil {
				t.Fatal("Validate() must fail")
			}
		})
	}
}

// TestInvitationExpired 过期判定为严格 After：到期时刻本身仍可用。
func TestInvitationExpired(t *testing.T) {
	inv := invitationFixture(t)
	if inv.Expired(fixedNow) {
		t.Fatal("fresh invitation must not be expired")
	}
	if !inv.Expired(inv.ExpiresAt.Add(time.Second)) {
		t.Fatal("invitation must expire after ExpiresAt")
	}
	if inv.Expired(inv.ExpiresAt) {
		t.Fatal("ExpiresAt itself is still valid (strict After)")
	}
	// 时区不影响判定：换算到 UTC+8 后比较的仍是同一时刻。
	if !inv.Expired(inv.ExpiresAt.Add(time.Second).In(time.FixedZone("UTC+8", 8*3600))) {
		t.Fatal("expiry must compare in UTC regardless of caller zone")
	}
}

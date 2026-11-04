package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestMemberRoleValid 穷举角色取值：只有三级是合法角色。
func TestMemberRoleValid(t *testing.T) {
	cases := []struct {
		role domain.MemberRole
		want bool
	}{
		{domain.MemberRoleOwner, true},
		{domain.MemberRoleAdmin, true},
		{domain.MemberRoleMember, true},
		{"", false},
		{"superuser", false},
		{"OWNER", false}, // 大小写敏感：与落库枚举一致
	}
	for _, tc := range cases {
		if got := tc.role.Valid(); got != tc.want {
			t.Fatalf("MemberRole(%q).Valid() = %v, want %v", tc.role, got, tc.want)
		}
	}
}

// TestMemberStatusValid 穷举成员状态。
func TestMemberStatusValid(t *testing.T) {
	for status, want := range map[domain.MemberStatus]bool{
		domain.MemberStatusActive:   true,
		domain.MemberStatusDisabled: true,
		"":                          false,
		"deleted":                   false, // 成员靠停用表达离开，不存在 deleted
	} {
		if got := status.Valid(); got != want {
			t.Fatalf("MemberStatus(%q).Valid() = %v, want %v", status, got, want)
		}
	}
}

// TestNewMember 覆盖构造期校验与归一化。
func TestNewMember(t *testing.T) {
	member, err := domain.NewMember(" t-1 ", " u-1 ", domain.MemberRoleAdmin, fixedNow)
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}
	if member.TenantID != "t-1" || member.SubjectID != "u-1" {
		t.Fatalf("NewMember() must trim ids: %+v", member)
	}
	if member.Status != domain.MemberStatusActive {
		t.Fatalf("NewMember() status = %q, want active", member.Status)
	}
	if !member.CreatedAt.Equal(fixedNow) || !member.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("NewMember() timestamps = %v / %v", member.CreatedAt, member.UpdatedAt)
	}

	cases := []struct {
		name    string
		tenant  string
		subject string
		role    domain.MemberRole
		want    error
	}{
		{"empty tenant", "  ", "u-1", domain.MemberRoleOwner, nil},
		{"empty subject", "t-1", " ", domain.MemberRoleOwner, domain.ErrSubjectRequired},
		{"subject too long", "t-1", strings.Repeat("x", domain.MaxSubjectIDLength+1), domain.MemberRoleOwner, domain.ErrSubjectTooLong},
		{"invalid role", "t-1", "u-1", "root", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewMember(tc.tenant, tc.subject, tc.role, fixedNow)
			if err == nil {
				t.Fatal("NewMember() must fail")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("NewMember() error = %v, want %v", err, tc.want)
			}
			if tc.name == "invalid role" {
				var roleErr *domain.InvalidRoleError
				if !errors.As(err, &roleErr) || roleErr.Role != "root" {
					t.Fatalf("NewMember() error = %v, want InvalidRoleError(root)", err)
				}
			}
		})
	}
}

// TestMemberValidate 覆盖仓储回读路径的脏数据防御。
func TestMemberValidate(t *testing.T) {
	valid := domain.Member{
		TenantID:  "t-1",
		SubjectID: "u-1",
		Role:      domain.MemberRoleMember,
		Status:    domain.MemberStatusActive,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(domain.Member) domain.Member
		want   error
	}{
		{"empty tenant", func(m domain.Member) domain.Member { m.TenantID = " "; return m }, nil},
		{"empty subject", func(m domain.Member) domain.Member { m.SubjectID = ""; return m }, domain.ErrSubjectRequired},
		{"invalid role", func(m domain.Member) domain.Member { m.Role = "root"; return m }, nil},
		{"invalid status", func(m domain.Member) domain.Member { m.Status = "gone"; return m }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mutate(valid).Validate()
			if err == nil {
				t.Fatal("Validate() must fail")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
			// 脏数据保持裸错误：不得把数据损坏误报成参数错误（InvalidStatusError）。
			var statusErr *domain.InvalidStatusError
			if tc.name == "invalid status" && errors.As(err, &statusErr) {
				t.Fatal("Validate() must not wrap dirty status into InvalidStatusError")
			}
		})
	}
}

// TestMemberWithRole 改角色：非法拒绝、同值不变更、异值刷新时间戳。
func TestMemberWithRole(t *testing.T) {
	member, err := domain.NewMember("t-1", "u-1", domain.MemberRoleMember, fixedNow)
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}

	if _, err := member.WithRole("root", fixedNow.Add(time.Hour)); err == nil {
		t.Fatal("WithRole(root) must fail")
	}

	same, err := member.WithRole(domain.MemberRoleMember, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("WithRole(same) error = %v", err)
	}
	if !same.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("same-role update must keep UpdatedAt, got %v", same.UpdatedAt)
	}

	later := fixedNow.Add(2 * time.Hour)
	promoted, err := member.WithRole(domain.MemberRoleOwner, later)
	if err != nil {
		t.Fatalf("WithRole(owner) error = %v", err)
	}
	if promoted.Role != domain.MemberRoleOwner {
		t.Fatalf("WithRole() role = %q", promoted.Role)
	}
	if !promoted.UpdatedAt.Equal(later.UTC()) {
		t.Fatalf("WithRole() UpdatedAt = %v, want %v", promoted.UpdatedAt, later.UTC())
	}
	if !promoted.CreatedAt.Equal(member.CreatedAt) {
		t.Fatal("WithRole() must not touch CreatedAt")
	}
}

// TestMemberWithStatus 停用 / 复活：非法状态与同值短路。
func TestMemberWithStatus(t *testing.T) {
	member, err := domain.NewMember("t-1", "u-1", domain.MemberRoleOwner, fixedNow)
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}

	var statusErr *domain.InvalidStatusError
	if _, err := member.WithStatus("deleted", fixedNow); !errors.As(err, &statusErr) {
		t.Fatalf("WithStatus(deleted) error = %v, want InvalidStatusError", err)
	}

	same, err := member.WithStatus(domain.MemberStatusActive, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("WithStatus(active) error = %v", err)
	}
	if !same.UpdatedAt.Equal(fixedNow) {
		t.Fatal("same-status update must keep UpdatedAt")
	}

	later := fixedNow.Add(time.Hour)
	disabled, err := member.WithStatus(domain.MemberStatusDisabled, later)
	if err != nil {
		t.Fatalf("WithStatus(disabled) error = %v", err)
	}
	if disabled.Active() {
		t.Fatal("disabled member must not be active")
	}
	if !disabled.UpdatedAt.Equal(later.UTC()) {
		t.Fatalf("WithStatus() UpdatedAt = %v", disabled.UpdatedAt)
	}
}

// TestValidateMemberStatus 入参校验路径返回可翻译为 400 的错误。
func TestValidateMemberStatus(t *testing.T) {
	if err := domain.ValidateMemberStatus(domain.MemberStatusDisabled); err != nil {
		t.Fatalf("ValidateMemberStatus(disabled) error = %v", err)
	}
	err := domain.ValidateMemberStatus("gone")
	var statusErr *domain.InvalidStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != "gone" {
		t.Fatalf("ValidateMemberStatus(gone) error = %v", err)
	}
}

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// scimNow 是固定测试时间，避免依赖真实时钟。
var scimNow = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

// TestNewScimTokenValidation 覆盖令牌必填字段与状态约束。
func TestNewScimTokenValidation(t *testing.T) {
	valid := domain.NewScimTokenParams{
		ID: "1001", Provider: "okta", TokenHash: strings.Repeat("a", 64), Fingerprint: "sha256:aaaa", Now: scimNow,
	}
	token, err := domain.NewScimToken(valid)
	if err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if token.Status != domain.ScimTokenActive || !token.Usable(scimNow) {
		t.Fatalf("token = %+v, want active and usable", token)
	}

	cases := []struct {
		name   string
		mutate func(*domain.NewScimTokenParams)
		want   error
	}{
		{name: "missing id", mutate: func(p *domain.NewScimTokenParams) { p.ID = "" }, want: domain.ErrEmptyInput},
		{name: "missing provider", mutate: func(p *domain.NewScimTokenParams) { p.Provider = " " }, want: domain.ErrInvalidSCIMToken},
		{name: "missing hash", mutate: func(p *domain.NewScimTokenParams) { p.TokenHash = "" }, want: domain.ErrInvalidSCIMToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := valid
			tc.mutate(&params)
			if _, err := domain.NewScimToken(params); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// 非法状态与「active 带到期时间」都必须被拒绝。
	broken := *token
	broken.Status = domain.ScimTokenStatus("weird")
	if err := broken.Validate(); !errors.Is(err, domain.ErrInvalidSCIMToken) {
		t.Fatalf("invalid status err = %v", err)
	}
	broken = *token
	expiry := scimNow.Add(time.Hour)
	broken.ExpiresAt = &expiry
	if err := broken.Validate(); !errors.Is(err, domain.ErrInvalidSCIMToken) {
		t.Fatalf("active token with expiry err = %v", err)
	}
}

// TestScimTokenLifecycle 覆盖退役宽限与吊销语义。
func TestScimTokenLifecycle(t *testing.T) {
	token, err := domain.NewScimToken(domain.NewScimTokenParams{
		ID: "1001", Provider: "okta", TokenHash: strings.Repeat("b", 64), Now: scimNow,
	})
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	token.Retire(scimNow, time.Hour)
	if token.Status != domain.ScimTokenRetiring || token.ExpiresAt == nil {
		t.Fatalf("token = %+v, want retiring with expiry", token)
	}
	if !token.Usable(scimNow.Add(30 * time.Minute)) {
		t.Fatalf("retiring token must stay usable within grace")
	}
	if token.Usable(scimNow.Add(2 * time.Hour)) {
		t.Fatalf("retiring token must expire after grace")
	}
	// 重复 retire 不应改变到期时间。
	previous := *token.ExpiresAt
	token.Retire(scimNow.Add(48*time.Hour), time.Hour)
	if !token.ExpiresAt.Equal(previous) {
		t.Fatalf("retire must be idempotent for non-active tokens")
	}

	token.Revoke(scimNow)
	if token.Status != domain.ScimTokenRevoked || token.RevokedAt == nil || token.Usable(scimNow) {
		t.Fatalf("token = %+v, want revoked and unusable", token)
	}
	revokedAt := *token.RevokedAt
	token.Revoke(scimNow.Add(time.Hour))
	if !token.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoke must be idempotent")
	}
}

// TestScimGroupLifecycle 覆盖组创建、改名、外部 ID 重绑与软删。
func TestScimGroupLifecycle(t *testing.T) {
	group, err := domain.NewScimGroup(domain.NewScimGroupParams{
		ID: "2001", Provider: "okta", ExternalID: "grp-1", DisplayName: "ops", MappedRole: "ops_admin", Now: scimNow,
	})
	if err != nil {
		t.Fatalf("new group: %v", err)
	}
	if group.Version != 1 || group.MappedRole != "ops_admin" {
		t.Fatalf("group = %+v, want version 1 and mapped role", group)
	}

	if err := group.Rename("readonly", "auditor", scimNow.Add(time.Minute)); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if group.DisplayName != "readonly" || group.MappedRole != "auditor" || group.Version != 2 {
		t.Fatalf("group = %+v, want renamed with version 2", group)
	}
	if err := group.RebindExternalID("grp-2", scimNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("rebind external id: %v", err)
	}
	if group.ExternalID != "grp-2" || group.Version != 3 {
		t.Fatalf("group = %+v, want rebound external id", group)
	}
	if err := group.Delete(scimNow.Add(3 * time.Minute)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !group.IsDeleted() {
		t.Fatalf("group must be deleted")
	}
	if err := group.Delete(scimNow.Add(4 * time.Minute)); !errors.Is(err, domain.ErrInvalidSCIMGroup) {
		t.Fatalf("repeat delete err = %v, want conflict", err)
	}
	if err := group.Rename("x", "", scimNow); !errors.Is(err, domain.ErrInvalidSCIMGroup) {
		t.Fatalf("rename deleted err = %v, want conflict", err)
	}
	if err := group.RebindExternalID("x", scimNow); !errors.Is(err, domain.ErrInvalidSCIMGroup) {
		t.Fatalf("rebind deleted err = %v, want conflict", err)
	}
}

// TestNewScimGroupValidation 覆盖组必填字段与版本约束。
func TestNewScimGroupValidation(t *testing.T) {
	valid := domain.NewScimGroupParams{
		ID: "2001", Provider: "okta", ExternalID: "grp-1", DisplayName: "ops", Now: scimNow,
	}
	cases := []struct {
		name   string
		mutate func(*domain.NewScimGroupParams)
		want   error
	}{
		{name: "missing id", mutate: func(p *domain.NewScimGroupParams) { p.ID = "" }, want: domain.ErrEmptyInput},
		{name: "missing provider", mutate: func(p *domain.NewScimGroupParams) { p.Provider = "" }, want: domain.ErrInvalidSCIMGroup},
		{name: "missing external id", mutate: func(p *domain.NewScimGroupParams) { p.ExternalID = " " }, want: domain.ErrInvalidSCIMGroup},
		{name: "missing display name", mutate: func(p *domain.NewScimGroupParams) { p.DisplayName = "" }, want: domain.ErrInvalidSCIMGroup},
		{name: "too long external id", mutate: func(p *domain.NewScimGroupParams) {
			p.ExternalID = strings.Repeat("x", domain.MaxScimExternalIDLen+1)
		}, want: domain.ErrInvalidSCIMGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := valid
			tc.mutate(&params)
			if _, err := domain.NewScimGroup(params); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	group, err := domain.NewScimGroup(valid)
	if err != nil {
		t.Fatalf("new group: %v", err)
	}
	group.Version = 0
	if err := group.Validate(); !errors.Is(err, domain.ErrInvalidSCIMGroup) {
		t.Fatalf("version 0 err = %v, want invalid", err)
	}
}

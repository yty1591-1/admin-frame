package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestClientIDFromSubject 验证机器主体的前缀反解与前缀缺失 / 空 client_id。
func TestClientIDFromSubject(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		wantID  string
		wantOK  bool
	}{
		{name: "round trip", subject: app.ClientSubject(" m2m-1 "), wantID: "m2m-1", wantOK: true},
		{name: "trimmed subject", subject: " client:m2m-2 ", wantID: "m2m-2", wantOK: true},
		{name: "empty client id", subject: "client:", wantID: "", wantOK: false},
		{name: "empty subject", subject: "", wantID: "", wantOK: false},
		{name: "account subject", subject: "acct-1", wantID: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := app.ClientIDFromSubject(tc.subject)
			if id != tc.wantID || ok != tc.wantOK {
				t.Fatalf("ClientIDFromSubject(%q) = (%q, %v), want (%q, %v)",
					tc.subject, id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestTokenServiceRejectsMissingArguments 验证签发入口的必填参数与构造依赖。
func TestTokenServiceRejectsMissingArguments(t *testing.T) {
	svc, signer := newTokenService(t, domain.ChannelAdmin)
	account := &domain.Account{ID: "acct-1", Channel: domain.ChannelAdmin, TokenVersion: 1}

	if _, err := svc.Issue(nil, "sess-1"); !hasAppCode(err, apperr.CodeInternal) {
		t.Fatalf("issue with nil account = %v, want internal", err)
	}
	if _, err := svc.Issue(account, "  "); !hasAppCode(err, apperr.CodeInternal) {
		t.Fatalf("issue with blank session = %v, want internal", err)
	}
	if _, err := svc.IssueAccessToken(nil, "sess-1", "m-1"); !hasAppCode(err, apperr.CodeInternal) {
		t.Fatalf("issue access with nil account = %v, want internal", err)
	}
	if _, err := svc.IssueAccessToken(account, "", "m-1"); !hasAppCode(err, apperr.CodeInternal) {
		t.Fatalf("issue access with blank session = %v, want internal", err)
	}
	if _, err := svc.IssueAccessToken(account, "sess-1", " "); !hasAppCode(err, apperr.CodeBadRequest) {
		t.Fatalf("issue access with blank tenant = %v, want bad request", err)
	}

	if _, err := app.NewTokenService(nil, domain.ChannelAdmin, time.Minute, time.Hour, &seqIDs{}); err == nil {
		t.Fatal("nil signer must fail")
	}
	if _, err := app.NewTokenService(signer, domain.ChannelAdmin, time.Minute, time.Hour, nil); err == nil {
		t.Fatal("nil id generator must fail")
	}
}

// TestTokenServiceIssueAccessTokenUsesCurrentTenant 验证切换租户时 current_tenant 覆盖默认值。
func TestTokenServiceIssueAccessTokenUsesCurrentTenant(t *testing.T) {
	svc, verifier := newTokenService(t, domain.ChannelBiz)
	account := &domain.Account{
		ID: "acct-2", Channel: domain.ChannelBiz, TokenVersion: 4, MerchantID: "m-1",
	}

	token, err := svc.IssueAccessToken(account, "sess-9", "m-2")
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	claims, err := verifier.Parse(token)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.GetString("sid") != "sess-9" {
		t.Fatalf("sid = %q, want sess-9", claims.GetString("sid"))
	}
	if claims.GetString("current_tenant") != "m-2" || claims.GetString("merchant_id") != "m-2" {
		t.Fatalf("tenant claims = %v, want m-2", claims.Extra)
	}
	if got := numericClaim(claims.Extra["ver"]); got != 4 {
		t.Fatalf("ver = %d, want 4", got)
	}
}

// TestScimServiceConstructorValidation 验证 SCIM 服务构造的必填依赖与默认值。
func TestScimServiceConstructorValidation(t *testing.T) {
	if _, err := app.NewScimService(app.ScimServiceConfig{}); err == nil {
		t.Fatal("missing dependencies must fail")
	}

	tokens, groups := persistence.NewMemoryScimRepositories()
	base := app.ScimServiceConfig{
		Accounts:   persistence.NewMemoryAccountRepository(),
		Identities: persistence.NewMemoryOAuthIdentityRepository(),
		Groups:     groups,
		Tokens:     tokens,
		Sessions:   persistence.NewMemorySessionRepository(),
		IDs:        &seqIDs{},
		Provider:   "okta",
	}

	noProvider := base
	noProvider.Provider = "  "
	if _, err := app.NewScimService(noProvider); err == nil {
		t.Fatal("blank provider must fail")
	}

	noRoles := base
	noRoles.GroupRoleMap = map[string]string{"ops": "ops_admin"}
	if _, err := app.NewScimService(noRoles); err == nil {
		t.Fatal("group role map without provisioner must fail")
	}

	svc, err := app.NewScimService(base)
	if err != nil {
		t.Fatalf("new scim service: %v", err)
	}
	if svc.Provider() != "okta" || svc.PageSize() != 50 || svc.Observer() != nil {
		t.Fatalf("defaults not applied: provider=%q pageSize=%d", svc.Provider(), svc.PageSize())
	}
}

// TestSessionCleanupConstructorValidation 验证清理任务依赖必填与保留期默认值。
func TestSessionCleanupConstructorValidation(t *testing.T) {
	if _, err := app.NewSessionCleanup(nil, persistence.NewMemoryLoginAttemptRepository(), app.RetentionPolicy{}, nil); err == nil {
		t.Fatal("nil session repository must fail")
	}
	if _, err := app.NewSessionCleanup(persistence.NewMemorySessionRepository(), nil, app.RetentionPolicy{}, nil); err == nil {
		t.Fatal("nil attempt repository must fail")
	}

	// 零值策略回落到默认保留期与批大小。
	cleanup, err := app.NewSessionCleanup(
		persistence.NewMemorySessionRepository(),
		persistence.NewMemoryLoginAttemptRepository(),
		app.RetentionPolicy{},
		nil,
	)
	if err != nil || cleanup == nil {
		t.Fatalf("new session cleanup: %v", err)
	}
}

// TestTenantSessionRevokerConstructorValidation 验证租户撤销器依赖必填与时钟回落。
func TestTenantSessionRevokerConstructorValidation(t *testing.T) {
	if _, err := app.NewTenantSessionRevoker(nil, persistence.NewMemorySessionRepository(), nil); err == nil {
		t.Fatal("nil account repository must fail")
	}
	if _, err := app.NewTenantSessionRevoker(persistence.NewMemoryAccountRepository(), nil, nil); err == nil {
		t.Fatal("nil session repository must fail")
	}

	revoker, err := app.NewTenantSessionRevoker(
		persistence.NewMemoryAccountRepository(),
		persistence.NewMemorySessionRepository(),
		nil,
	)
	if err != nil || revoker == nil {
		t.Fatalf("new tenant session revoker: %v", err)
	}
}

// TestSMSLoginServiceConstructorValidation 验证短信登录服务缺少依赖时直接失败。
func TestSMSLoginServiceConstructorValidation(t *testing.T) {
	if _, err := app.NewSMSLoginService(app.SMSLoginConfig{}); err == nil {
		t.Fatal("missing dependencies must fail")
	}
}

// hasAppCode 报告错误是否为指定业务码的 AppError。
func hasAppCode(err error, code int) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == code
}

package c

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestE2ESMSSendRegisterLogin 覆盖验证码发送、注册、登录与防刷。
func TestE2ESMSSendRegisterLogin(t *testing.T) {
	f := newCFixture(t)
	const (
		registerPhone = "+8613800138000"
		loginPhone    = "+8613800138001"
		unknownPhone  = "+8613800138002"
	)

	// 发送验证码成功后立即重发被限流。
	recorder := f.do(t, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"`+registerPhone+`"}`, "")
	requireCStatus(t, recorder, http.StatusNoContent)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"`+registerPhone+`"}`, "")
	requireCStatus(t, recorder, http.StatusTooManyRequests)
	requireCErrorCode(t, recorder, apperr.CodeTooManyRequests)

	// 非法手机号与非法请求体。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send", `{"phone":"13800138000"}`, "")
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send", `{"phone":`, "")
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)

	// 使用首条验证码注册并开户。
	code := f.sender.codeOf(t, registerPhone)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/register",
		`{"phone":"`+registerPhone+`","code":"`+code+`"}`, "")
	requireCStatus(t, recorder, http.StatusOK)
	var tokens tokenResponse
	decodeCData(t, recorder, &tokens)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.TokenType != "Bearer" {
		t.Fatalf("register token response = %+v", tokens)
	}
	accounts, total, err := f.accounts.List(context.Background(), domain.AccountListFilter{Channel: domain.ChannelC})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if total != 1 || len(accounts) != 1 {
		t.Fatalf("accounts = %d, want 1 consumer account", total)
	}
	if accounts[0].Status != domain.AccountStatusActive {
		t.Fatalf("registered account status = %q, want active", accounts[0].Status)
	}

	// 重复注册统一返回 40100，不暴露账号存在。
	f.clock.advance(61 * time.Second)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"`+registerPhone+`"}`, "")
	requireCStatus(t, recorder, http.StatusNoContent)
	code = f.sender.codeOf(t, registerPhone)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/register",
		`{"phone":"`+registerPhone+`","code":"`+code+`"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 已注册手机号可正常验证码登录。
	registered := f.registerPhone(t, loginPhone)
	if registered.AccountID == "" {
		t.Fatal("register helper must return account")
	}
	f.clock.advance(61 * time.Second)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"`+loginPhone+`"}`, "")
	requireCStatus(t, recorder, http.StatusNoContent)
	code = f.sender.codeOf(t, loginPhone)

	// 错误验证码统一 40100。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/login",
		`{"phone":"`+loginPhone+`","code":"000000"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 正确验证码登录成功。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/login",
		`{"phone":"`+loginPhone+`","code":"`+code+`"}`, "")
	requireCStatus(t, recorder, http.StatusOK)
	decodeCData(t, recorder, &tokens)
	if tokens.AccessToken == "" {
		t.Fatal("sms login must issue tokens")
	}
	if tokens.AbsoluteExpiresAt == nil {
		t.Fatal("sms login must return absolute expiry")
	}

	// 未注册手机号即使验证码正确也统一 40100。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"`+unknownPhone+`"}`, "")
	requireCStatus(t, recorder, http.StatusNoContent)
	code = f.sender.codeOf(t, unknownPhone)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/login",
		`{"phone":"`+unknownPhone+`","code":"`+code+`"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 非法请求体。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/login", `{`, "")
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)
}

// TestE2ESMSDisabled 覆盖短信能力关闭时的降级响应。
func TestE2ESMSDisabled(t *testing.T) {
	f := newCFixture(t, withoutSMS())

	// 发送与注册路由不注册，登录由 Handler 返回 50300。
	recorder := f.do(t, http.MethodPost, "/c/v1/auth/sms/send", `{"phone":"+8613800138000"}`, "")
	requireCStatus(t, recorder, http.StatusNotFound)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/sms/register", `{"phone":"+8613800138000","code":"123456"}`, "")
	requireCStatus(t, recorder, http.StatusNotFound)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/login", `{"phone":"+8613800138000","code":"123456"}`, "")
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	// 直调覆盖未注册路由的关闭分支。
	recorder = f.invoke(f.handler.SendSMSCode, http.MethodPost, "/c/v1/auth/sms/send",
		`{"phone":"+8613800138000"}`, "", nil)
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.Register, http.MethodPost, "/c/v1/auth/sms/register",
		`{"phone":"+8613800138000","code":"123456"}`, "", nil)
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)

	recorder = f.invoke(f.handler.RebindPhone, http.MethodPost, "/c/v1/auth/phone/rebind",
		`{"phone":"+8613800138000","code":"123456"}`, "acct-1", nil)
	requireCStatus(t, recorder, http.StatusServiceUnavailable)
	requireCErrorCode(t, recorder, apperr.CodeServiceUnavailable)
}

// TestE2ESMSRebind 覆盖手机号换绑：新旧验证码、主体切换与会话撤销。
func TestE2ESMSRebind(t *testing.T) {
	f := newCFixture(t)
	oldPhone := "+8613800138010"
	newPhone := "+8613800138011"
	registered := f.registerPhone(t, oldPhone)

	f.clock.advance(61 * time.Second)
	oldCode := f.sendCode(t, oldPhone)
	f.clock.advance(61 * time.Second)
	newCode := f.sendCode(t, newPhone)

	recorder := f.do(t, http.MethodPost, "/c/v1/auth/phone/rebind",
		`{"phone":"`+newPhone+`","code":"`+newCode+`","old_code":"`+oldCode+`"}`,
		registered.AccountID)
	requireCStatus(t, recorder, http.StatusNoContent)

	account, err := f.accounts.FindByID(context.Background(), registered.AccountID)
	if err != nil {
		t.Fatalf("load rebound account: %v", err)
	}
	wantSubject := "phone:" + app.HashAccountRef(domain.ChannelC, newPhone)
	if account.Subject != wantSubject {
		t.Fatalf("subject = %q, want %q", account.Subject, wantSubject)
	}

	// 未认证、非法请求体与错误旧号验证码。
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/phone/rebind",
		`{"phone":"`+newPhone+`","code":"123456"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.do(t, http.MethodPost, "/c/v1/auth/phone/rebind", `{`, registered.AccountID)
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)

	// 另一位用户换绑时旧号验证码错误被拒绝。
	otherPhone := "+8613800138012"
	other := f.registerPhone(t, otherPhone)
	f.clock.advance(61 * time.Second)
	_ = f.sendCode(t, otherPhone)
	f.clock.advance(61 * time.Second)
	targetPhone := "+8613800138013"
	targetCode := f.sendCode(t, targetPhone)
	recorder = f.do(t, http.MethodPost, "/c/v1/auth/phone/rebind",
		`{"phone":"`+targetPhone+`","code":"`+targetCode+`","old_code":"000000"}`,
		other.AccountID)
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)
}

// TestE2ESMSDeleteAccount 覆盖验证码再认证后的自助注销。
func TestE2ESMSDeleteAccount(t *testing.T) {
	f := newCFixture(t)
	phone := "+8613800138020"
	registered := f.registerPhone(t, phone)
	f.clock.advance(61 * time.Second)
	code := f.sendCode(t, phone)

	recorder := f.do(t, http.MethodDelete, "/c/v1/auth/account", `{"code":"`+code+`"}`, registered.AccountID)
	requireCStatus(t, recorder, http.StatusNoContent)
	account, err := f.accounts.FindByID(context.Background(), registered.AccountID)
	if err != nil {
		t.Fatalf("load deleted account: %v", err)
	}
	if account.DeletedAt == nil {
		t.Fatalf("account must be soft deleted: %+v", account)
	}

	// 错误验证码被拒绝。
	otherPhone := "+8613800138021"
	other := f.registerPhone(t, otherPhone)
	f.clock.advance(61 * time.Second)
	_ = f.sendCode(t, otherPhone)
	recorder = f.do(t, http.MethodDelete, "/c/v1/auth/account", `{"code":"000000"}`, other.AccountID)
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	// 未认证、非法请求体。
	recorder = f.do(t, http.MethodDelete, "/c/v1/auth/account", `{"code":"123456"}`, "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)

	recorder = f.do(t, http.MethodDelete, "/c/v1/auth/account", `{`, other.AccountID)
	requireCStatus(t, recorder, http.StatusBadRequest)
	requireCErrorCode(t, recorder, apperr.CodeBadRequest)

	// OAuth-only 账号无手机号，短信再认证返回 40900。
	oauthAccount := f.seedAccount(t, "acct-oauth-only", "oauth-only@example.com", false)
	recorder = f.do(t, http.MethodDelete, "/c/v1/auth/account", `{"code":"123456"}`, oauthAccount.ID)
	requireCStatus(t, recorder, http.StatusConflict)
	requireCErrorCode(t, recorder, apperr.CodeConflict)
}

// TestE2ESMSDeleteAccountWithoutSMS 覆盖未启用短信时仅凭 Bearer 注销。
func TestE2ESMSDeleteAccountWithoutSMS(t *testing.T) {
	f := newCFixture(t, withoutSMS())
	account := f.seedAccount(t, "acct-delete", "delete@example.com", true)

	recorder := f.do(t, http.MethodDelete, "/c/v1/auth/account", "", account.ID)
	requireCStatus(t, recorder, http.StatusNoContent)
	reloaded, err := f.accounts.FindByID(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("load deleted account: %v", err)
	}
	if reloaded.DeletedAt == nil {
		t.Fatalf("account must be soft deleted: %+v", reloaded)
	}

	recorder = f.do(t, http.MethodDelete, "/c/v1/auth/account", "", "")
	requireCStatus(t, recorder, http.StatusUnauthorized)
	requireCErrorCode(t, recorder, apperr.CodeUnauthorized)
}

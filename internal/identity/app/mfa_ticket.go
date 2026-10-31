package app

import (
	"context"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// 票据有效期（docs/identity-authn.md §7.2、§7.2.1）。
const (
	// MFATicketTTL 是两步登录票据有效期。
	MFATicketTTL = 5 * time.Minute
	// MFASetupTicketTTL 是强制绑定票据有效期；绑定需扫码与录入首码，故长于两步登录。
	MFASetupTicketTTL = 10 * time.Minute
	// PasswordChangeTicketTTL 是强制改密票据有效期。
	PasswordChangeTicketTTL = 5 * time.Minute
)

// 票据声明（docs/identity-authn.md §7.2、§7.2.1）。
const (
	// mfaTicketScope 两步登录票据 scope，只允许调用 mfa/verify。
	mfaTicketScope = "mfa"
	// mfaSetupTicketScope 强制绑定票据 scope，只允许调用 mfa/setup 与 mfa/verify。
	mfaSetupTicketScope = "mfa_setup"
	// passwordChangeTicketScope 强制改密票据 scope，只允许调用密码修改端点。
	passwordChangeTicketScope = "password_change"
)

// MFATicket 是一次两步登录挑战签发的短期票据。
//
// 票据原文只返回给客户端一次；JTI 用于 Redis 一次性消费，禁止写日志。
type MFATicket struct {
	Ticket    string
	JTI       string
	ExpiresAt time.Time
}

// MFATicketStore 是短期票据的一次性消费端口。
//
// Consume 返回 true 表示首次消费成功；返回 false 表示票据已被使用（重放）。
// 存储不可用时必须返回错误，由调用方按 fail_closed 输出 50300。
type MFATicketStore interface {
	Consume(ctx context.Context, jti string, ttl time.Duration) (bool, error)
}

// IssueMFATicket 为 A 端账号签发两步登录票据。
func (s *TokenService) IssueMFATicket(accountID string) (MFATicket, error) {
	return s.issueScopedTicket(accountID, mfaTicketScope, MFATicketTTL)
}

// IssueMFASetupTicket 为 A 端账号签发强制绑定票据（docs/identity-authn.md §7.2.1）。
//
// 票据只允许调用 mfa/setup 与 mfa/verify：scope=mfa_setup、有效期 10 分钟、jti 一次性消费。
func (s *TokenService) IssueMFASetupTicket(accountID string) (MFATicket, error) {
	return s.issueScopedTicket(accountID, mfaSetupTicketScope, MFASetupTicketTTL)
}

// issueScopedTicket 签发短期票据。
//
// 票据只包含 scope / channel，不写 ver / sid：即使被当作 Bearer 使用，
// 撤销检查器也会因缺少 ver 声明拒绝（setup 票据同样不具备业务访问能力）。
func (s *TokenService) issueScopedTicket(accountID, scope string, ttl time.Duration) (MFATicket, error) {
	if strings.TrimSpace(accountID) == "" {
		return MFATicket{}, apperr.Internal("mfa ticket account id is required", nil)
	}
	if s.channel != domain.ChannelAdmin && s.channel != domain.ChannelBiz {
		return MFATicket{}, apperr.Internal("mfa ticket is only supported on admin/biz channel", nil)
	}
	now := s.clock.Now().UTC()
	jti, err := s.ids.NextString()
	if err != nil {
		return MFATicket{}, apperr.Internal("generate mfa ticket id failed", err)
	}
	claims := jwt.Claims{
		Subject:   accountID,
		Audience:  []string{string(s.channel)},
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
		ID:        jti,
		Extra: map[string]any{
			"scope":   scope,
			"channel": string(s.channel),
		},
	}
	ticket, err := s.signer.Sign(claims)
	if err != nil {
		return MFATicket{}, apperr.Internal("sign mfa ticket failed", err)
	}
	return MFATicket{Ticket: ticket, JTI: jti, ExpiresAt: claims.ExpiresAt}, nil
}

// ParseMFATicket 校验并解析两步登录票据。
//
// 只接受当前端别（admin / biz）、scope=mfa 的票据；失败统一 40100，不区分具体原因。
func (s *TokenService) ParseMFATicket(raw string) (jwt.Claims, error) {
	return s.parseScopedTicket(raw, mfaTicketScope)
}

// ParseMFASetupTicket 校验并解析强制绑定票据。
//
// 与 ParseMFATicket 隔离 scope：两步登录票据不能用于绑定，绑定票据也不能用于两步登录。
func (s *TokenService) ParseMFASetupTicket(raw string) (jwt.Claims, error) {
	return s.parseScopedTicket(raw, mfaSetupTicketScope)
}

// parseScopedTicket 校验签名、端别与 scope。
func (s *TokenService) parseScopedTicket(raw, scope string) (jwt.Claims, error) {
	if s.parser == nil {
		return jwt.Claims{}, apperr.Internal("mfa ticket parser is not configured", nil)
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return jwt.Claims{}, invalidToken()
	}
	claims, err := s.parser.Parse(token)
	if err != nil {
		return jwt.Claims{}, invalidToken()
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.ID) == "" {
		return jwt.Claims{}, invalidToken()
	}
	if claims.GetString("scope") != scope {
		return jwt.Claims{}, invalidToken()
	}
	if claims.GetString("channel") != string(s.channel) {
		return jwt.Claims{}, invalidToken()
	}
	if !containsAudience(claims.Audience, string(s.channel)) {
		return jwt.Claims{}, invalidToken()
	}
	return claims, nil
}

// containsAudience 报告 audience 是否包含目标端别。
func containsAudience(audience []string, target string) bool {
	for _, value := range audience {
		if value == target {
			return true
		}
	}
	return false
}

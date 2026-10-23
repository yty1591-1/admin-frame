package app

import (
	"context"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// MFATicketTTL 是两步登录票据有效期（docs/identity-authn.md §7.2）。
const MFATicketTTL = 5 * time.Minute

// 票据声明（docs/identity-authn.md §7.2）。
const (
	// mfaTicketScope 票据 scope，只允许调用 mfa/verify。
	mfaTicketScope = "mfa"
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
//
// 票据只包含 scope / channel，不写 ver / sid：即使被当作 Bearer 使用，
// 阶段 2 的撤销检查器也会因缺少 ver 声明拒绝。
func (s *TokenService) IssueMFATicket(accountID string) (MFATicket, error) {
	if strings.TrimSpace(accountID) == "" {
		return MFATicket{}, apperr.Internal("mfa ticket account id is required", nil)
	}
	if s.channel != domain.ChannelAdmin {
		return MFATicket{}, apperr.Internal("mfa ticket is only supported on admin channel", nil)
	}
	now := s.clock.Now().UTC()
	jti, err := s.ids.NextString()
	if err != nil {
		return MFATicket{}, apperr.Internal("generate mfa ticket id failed", err)
	}
	claims := jwt.Claims{
		Subject:   accountID,
		Audience:  []string{string(domain.ChannelAdmin)},
		IssuedAt:  now,
		ExpiresAt: now.Add(MFATicketTTL),
		ID:        jti,
		Extra: map[string]any{
			"scope":   mfaTicketScope,
			"channel": string(domain.ChannelAdmin),
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
// 只接受 admin 端、scope=mfa 的票据；失败统一 40100，不区分具体原因。
func (s *TokenService) ParseMFATicket(raw string) (jwt.Claims, error) {
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
	if claims.GetString("scope") != mfaTicketScope {
		return jwt.Claims{}, invalidToken()
	}
	if claims.GetString("channel") != string(domain.ChannelAdmin) {
		return jwt.Claims{}, invalidToken()
	}
	if !containsAudience(claims.Audience, string(domain.ChannelAdmin)) {
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

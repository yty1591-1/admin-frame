package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// 刷新令牌前缀：按端别区分类型，便于日志识别且不泄露内容。
const (
	// RefreshPrefixAdmin A 端刷新令牌前缀。
	RefreshPrefixAdmin = "ar_"
	// RefreshPrefixBiz B 端刷新令牌前缀。
	RefreshPrefixBiz = "br_"
	// RefreshPrefixC C 端刷新令牌前缀。
	RefreshPrefixC = "cr_"
	// refreshTokenBytes 刷新令牌随机字节数（32 字节 = 256 bit）。
	refreshTokenBytes = 32
)

// 令牌生命周期哨兵错误。
var (
	// ErrInvalidTokenTTL 令牌有效期配置非法。
	ErrInvalidTokenTTL = errors.New("invalid token ttl")
	// ErrInvalidRefreshToken 刷新令牌格式非法或端别不匹配。
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
)

// Clock 是令牌服务使用的可注入时钟。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

// Now 返回当前 UTC 时间。
func (realClock) Now() time.Time { return time.Now().UTC() }

// TokenSigner 是 Access Token 签发端口；*jwt.Manager 直接满足。
type TokenSigner interface {
	Sign(claims jwt.Claims) (string, error)
}

// TokenParser 是短期票据校验端口；*jwt.Manager 直接满足。
type TokenParser interface {
	Parse(token string) (jwt.Claims, error)
}

// TokenPair 是一次签发产生的访问令牌与刷新令牌。
//
// RefreshHash 仅供仓储持久化，禁止返回给客户端或写入日志。
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	RefreshHash  string
	ExpiresIn    int64
	// AbsoluteExpiresAt 设备绝对到期时间；刷新不得延长，由 AuthService 填充。
	AbsoluteExpiresAt time.Time
}

// TokenService 负责 Access JWT 与一次性刷新令牌的生成。
//
// 约束（docs/identity-authn.md §4）：
//   - exp - iat 固定等于 access_ttl，不接受调用方指定；
//   - 令牌不携带 roles，避免 IAM 撤权滞后；
//   - 刷新令牌只出现一次，库内只存 SHA-256 摘要。
type TokenService struct {
	signer     TokenSigner
	parser     TokenParser
	channel    domain.Channel
	accessTTL  time.Duration
	refreshTTL time.Duration
	ids        IDGenerator
	clock      Clock
}

// NewTokenService 构造令牌服务；signer / ids 必填，TTL 须为正。
func NewTokenService(
	signer TokenSigner,
	channel domain.Channel,
	accessTTL, refreshTTL time.Duration,
	ids IDGenerator,
	opts ...TokenServiceOption,
) (*TokenService, error) {
	if signer == nil || ids == nil {
		return nil, apperr.Internal("token service dependencies are required", nil)
	}
	if err := channel.Validate(); err != nil {
		return nil, err
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, fmt.Errorf("%w: access and refresh ttl must be positive", ErrInvalidTokenTTL)
	}
	if refreshTTL <= accessTTL {
		return nil, fmt.Errorf("%w: refresh ttl must be greater than access ttl", ErrInvalidTokenTTL)
	}
	svc := &TokenService{
		signer:     signer,
		channel:    channel,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		ids:        ids,
		clock:      realClock{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc, nil
}

// TokenServiceOption 配置 TokenService。
type TokenServiceOption func(*TokenService)

// WithTokenClock 注入时钟（测试用）。
func WithTokenClock(clock Clock) TokenServiceOption {
	return func(s *TokenService) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithTokenParser 注入票据校验器；未注入时 ParseMFATicket 返回内部错误。
func WithTokenParser(parser TokenParser) TokenServiceOption {
	return func(s *TokenService) {
		if parser != nil {
			s.parser = parser
		}
	}
}

// AccessTTL 返回访问令牌有效期。
func (s *TokenService) AccessTTL() time.Duration { return s.accessTTL }

// RefreshTTL 返回刷新令牌有效期。
func (s *TokenService) RefreshTTL() time.Duration { return s.refreshTTL }

// Channel 返回令牌端别。
func (s *TokenService) Channel() domain.Channel { return s.channel }

// Issue 为账号签发一对令牌；sessionID 写入 sid 声明用于单会话撤销。
func (s *TokenService) Issue(account *domain.Account, sessionID string) (TokenPair, error) {
	if account == nil {
		return TokenPair{}, apperr.Internal("account is required", nil)
	}
	if strings.TrimSpace(sessionID) == "" {
		return TokenPair{}, apperr.Internal("session id is required", nil)
	}
	refresh, err := GenerateRefreshToken(s.channel)
	if err != nil {
		return TokenPair{}, err
	}
	hash, err := HashRefreshToken(refresh)
	if err != nil {
		return TokenPair{}, err
	}
	access, err := s.signAccessToken(account, sessionID, "")
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		RefreshHash:  hash,
		ExpiresIn:    int64(s.accessTTL / time.Second),
	}, nil
}

// IssueAccessToken 为同会话重签访问令牌（T-22 租户切换）。
//
// currentTenant 写入 current_tenant 声明；会话（sid）与撤销版本不变——
// 切换租户是授权上下文变更，不是重新认证，不产生新会话也不作废旧令牌：
// 旧 access 自然过期，生效租户回落 merchant_id（见 auth.Principal.CurrentTenant）。
func (s *TokenService) IssueAccessToken(account *domain.Account, sessionID, currentTenant string) (string, error) {
	if account == nil {
		return "", apperr.Internal("account is required", nil)
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", apperr.Internal("session id is required", nil)
	}
	if strings.TrimSpace(currentTenant) == "" {
		return "", apperr.BadRequest("current tenant is required")
	}
	return s.signAccessToken(account, sessionID, currentTenant)
}

// signAccessToken 签发访问令牌；currentTenant 非空时覆盖默认生效租户。
func (s *TokenService) signAccessToken(account *domain.Account, sessionID, currentTenant string) (string, error) {
	now := s.clock.Now().UTC()
	jti, err := s.ids.NextString()
	if err != nil {
		return "", apperr.Internal("generate token id failed", err)
	}
	extra := map[string]any{
		"ver":     account.TokenVersion,
		"channel": string(s.channel),
		"sid":     sessionID,
	}
	if s.channel == domain.ChannelBiz {
		merchant := account.MerchantID
		if trimmed := strings.TrimSpace(currentTenant); trimmed != "" {
			merchant = trimmed
		}
		// T-22：current_tenant 与 merchant_id 同值签发——merchant_id 保留
		// 是旧令牌兼容口径（回落链），新声明是唯一权威来源。
		extra["merchant_id"] = merchant
		extra["current_tenant"] = merchant
	}
	claims := jwt.Claims{
		Subject:   account.ID,
		Audience:  []string{string(s.channel)},
		IssuedAt:  now,
		ExpiresAt: now.Add(s.accessTTL),
		ID:        jti,
		Extra:     extra,
	}
	access, err := s.signer.Sign(claims)
	if err != nil {
		return "", apperr.Internal("sign access token failed", err)
	}
	return access, nil
}

// GenerateRefreshToken 生成指定端别的 32 字节随机刷新令牌。
func GenerateRefreshToken(channel domain.Channel) (string, error) {
	prefix, err := refreshPrefix(channel)
	if err != nil {
		return "", err
	}
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", apperr.Internal("generate refresh token failed", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashRefreshToken 校验刷新令牌格式并返回 SHA-256 十六进制摘要。
//
// 只接受 32 字节随机数 + 端别前缀；库内只保存该摘要。
func HashRefreshToken(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	prefix, rest, ok := splitRefreshPrefix(trimmed)
	if !ok {
		return "", fmt.Errorf("%w: unsupported prefix", ErrInvalidRefreshToken)
	}
	_ = prefix
	decoded, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(decoded) != refreshTokenBytes {
		return "", fmt.Errorf("%w: malformed payload", ErrInvalidRefreshToken)
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:]), nil
}

// RefreshPrefixMatches 报告刷新令牌前缀是否与端别一致。
func RefreshPrefixMatches(channel domain.Channel, raw string) bool {
	prefix, err := refreshPrefix(channel)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(raw), prefix)
}

// refreshPrefix 返回端别对应的刷新令牌前缀。
func refreshPrefix(channel domain.Channel) (string, error) {
	switch channel {
	case domain.ChannelAdmin:
		return RefreshPrefixAdmin, nil
	case domain.ChannelBiz:
		return RefreshPrefixBiz, nil
	case domain.ChannelC:
		return RefreshPrefixC, nil
	default:
		return "", fmt.Errorf("%w: unsupported channel", ErrInvalidRefreshToken)
	}
}

// splitRefreshPrefix 拆分已知前缀与随机部分。
func splitRefreshPrefix(raw string) (prefix, rest string, ok bool) {
	for _, p := range []string{RefreshPrefixAdmin, RefreshPrefixBiz, RefreshPrefixC} {
		if strings.HasPrefix(raw, p) {
			return p, strings.TrimPrefix(raw, p), true
		}
	}
	return "", "", false
}

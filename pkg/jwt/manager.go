package jwt

import (
	"crypto/rsa"
	stderrors "errors"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// Algorithm 支持的签名算法。
type Algorithm string

const (
	// AlgorithmHS256 HMAC-SHA256（对称密钥；适合内网/单服务，密钥须轮换管控）。
	AlgorithmHS256 Algorithm = "HS256"
	// AlgorithmRS256 RSA-SHA256（非对称；推荐生产多端独立密钥对）。
	AlgorithmRS256 Algorithm = "RS256"
)

// Manager 同时提供签发与校验；并发安全（无可变共享状态）。
type Manager struct {
	alg       Algorithm
	issuer    string
	audience  []string
	ttl       time.Duration
	leeway    time.Duration
	clock     Clock
	hmacKey   []byte
	hmacAlts  [][]byte
	rsaPriv   *rsa.PrivateKey
	rsaPub    *rsa.PublicKey
	rsaPubAlt []*rsa.PublicKey
}

// HMACConfig 配置 HMAC 管理器。
type HMACConfig struct {
	// Issuer 期望/默认 iss；校验时必填且必须匹配。
	Issuer string
	// Audience 期望/默认 aud；至少一项；校验时 Token 须命中其一。
	Audience []string
	// Secret 当前 HMAC 密钥；长度须 ≥32 字节。
	Secret []byte
	// Secrets 额外可接受的旧密钥（仅校验，不用于签发）。
	Secrets [][]byte
	// TTL 签发时默认存活期；须 >0。
	TTL time.Duration
	// Leeway 校验时钟偏移容忍；默认 0。
	Leeway time.Duration
	// Clock 可注入时钟。
	Clock Clock
}

// RSAConfig 配置 RSA 管理器。
type RSAConfig struct {
	Issuer   string
	Audience []string
	// PrivateKey 用于签发；仅校验时可为空。
	PrivateKey *rsa.PrivateKey
	// PublicKey 用于校验；若为空且 PrivateKey 非空则取其公钥。
	PublicKey *rsa.PublicKey
	// PublicKeys 额外可接受的旧公钥（轮换）。
	PublicKeys []*rsa.PublicKey
	TTL        time.Duration
	Leeway     time.Duration
	Clock      Clock
}

// NewHMAC 创建 HS256 Manager。
func NewHMAC(cfg HMACConfig) (*Manager, error) {
	if err := validateCommon(cfg.Issuer, cfg.Audience, cfg.TTL); err != nil {
		return nil, err
	}
	if len(cfg.Secret) == 0 {
		return nil, ErrMissingSecret
	}
	if len(cfg.Secret) < 32 {
		return nil, fmt.Errorf("%w: secret should be at least 32 bytes", ErrMissingSecret)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock()
	}
	alts := make([][]byte, 0, len(cfg.Secrets))
	for _, s := range cfg.Secrets {
		if len(s) > 0 {
			alts = append(alts, append([]byte(nil), s...))
		}
	}
	return &Manager{
		alg:      AlgorithmHS256,
		issuer:   cfg.Issuer,
		audience: append([]string(nil), cfg.Audience...),
		ttl:      cfg.TTL,
		leeway:   cfg.Leeway,
		clock:    clock,
		hmacKey:  append([]byte(nil), cfg.Secret...),
		hmacAlts: alts,
	}, nil
}

// NewRSA 创建 RS256 Manager。
func NewRSA(cfg RSAConfig) (*Manager, error) {
	if err := validateCommon(cfg.Issuer, cfg.Audience, cfg.TTL); err != nil {
		return nil, err
	}
	pub := cfg.PublicKey
	if pub == nil && cfg.PrivateKey != nil {
		pub = &cfg.PrivateKey.PublicKey
	}
	if pub == nil {
		return nil, ErrMissingKey
	}
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock()
	}
	alts := make([]*rsa.PublicKey, 0, len(cfg.PublicKeys))
	for _, k := range cfg.PublicKeys {
		if k != nil {
			alts = append(alts, k)
		}
	}
	return &Manager{
		alg:       AlgorithmRS256,
		issuer:    cfg.Issuer,
		audience:  append([]string(nil), cfg.Audience...),
		ttl:       cfg.TTL,
		leeway:    cfg.Leeway,
		clock:     clock,
		rsaPriv:   cfg.PrivateKey,
		rsaPub:    pub,
		rsaPubAlt: alts,
	}, nil
}

func validateCommon(issuer string, audience []string, ttl time.Duration) error {
	if issuer == "" {
		return validationErr(ErrInvalidIssuer, "issuer is required")
	}
	if len(audience) == 0 {
		return validationErr(ErrInvalidAudience, "audience is required")
	}
	for _, a := range audience {
		if a == "" {
			return validationErr(ErrInvalidAudience, "audience contains empty value")
		}
	}
	if ttl <= 0 {
		return fmt.Errorf("jwt: TTL must be > 0")
	}
	return nil
}

// Algorithm 返回签名算法。
func (m *Manager) Algorithm() Algorithm {
	if m == nil {
		return ""
	}
	return m.alg
}

// Issuer 返回配置的 iss。
func (m *Manager) Issuer() string {
	if m == nil {
		return ""
	}
	return m.issuer
}

// Audience 返回配置的 aud 副本。
func (m *Manager) Audience() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.audience...)
}

// Sign 签发 Token；不得将返回值写入日志原文（SEC-05）。
func (m *Manager) Sign(claims Claims) (string, error) {
	if m == nil {
		return "", ErrInvalidToken
	}
	now := m.clock.Now().UTC()
	mc, err := claims.toMap(now, m.issuer, m.audience, m.ttl)
	if err != nil {
		return "", err
	}

	switch m.alg {
	case AlgorithmHS256:
		tok := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, mc)
		return tok.SignedString(m.hmacKey)
	case AlgorithmRS256:
		if m.rsaPriv == nil {
			return "", ErrMissingKey
		}
		tok := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, mc)
		return tok.SignedString(m.rsaPriv)
	default:
		return "", ErrUnsupportedAlgorithm
	}
}

// Parse 校验并解析 Token；失败返回可 errors.Is 的哨兵错误。
//
// 安全约束：仅接受 Manager 配置算法；强制校验 iss / aud / exp；拒绝 alg=none。
func (m *Manager) Parse(tokenString string) (Claims, error) {
	var zero Claims
	if m == nil || tokenString == "" {
		return zero, validationErr(ErrInvalidToken, "empty token")
	}
	switch m.alg {
	case AlgorithmHS256:
		return m.parseWithKeys(tokenString, m.hmacKeyFuncs())
	case AlgorithmRS256:
		return m.parseWithKeys(tokenString, m.rsaKeyFuncs())
	default:
		return zero, ErrUnsupportedAlgorithm
	}
}

func (m *Manager) hmacKeyFuncs() []jwtv5.Keyfunc {
	keys := make([][]byte, 0, 1+len(m.hmacAlts))
	keys = append(keys, m.hmacKey)
	keys = append(keys, m.hmacAlts...)
	out := make([]jwtv5.Keyfunc, 0, len(keys))
	for _, key := range keys {
		k := key
		out = append(out, func(t *jwtv5.Token) (any, error) {
			if t.Method.Alg() != string(AlgorithmHS256) {
				return nil, ErrUnsupportedAlgorithm
			}
			return k, nil
		})
	}
	return out
}

func (m *Manager) rsaKeyFuncs() []jwtv5.Keyfunc {
	pubs := make([]*rsa.PublicKey, 0, 1+len(m.rsaPubAlt))
	if m.rsaPub != nil {
		pubs = append(pubs, m.rsaPub)
	}
	pubs = append(pubs, m.rsaPubAlt...)
	out := make([]jwtv5.Keyfunc, 0, len(pubs))
	for _, pub := range pubs {
		p := pub
		out = append(out, func(t *jwtv5.Token) (any, error) {
			if t.Method.Alg() != string(AlgorithmRS256) {
				return nil, ErrUnsupportedAlgorithm
			}
			return p, nil
		})
	}
	return out
}

func (m *Manager) parseWithKeys(tokenString string, keyFuncs []jwtv5.Keyfunc) (Claims, error) {
	var last error
	for _, kf := range keyFuncs {
		parser := jwtv5.NewParser(
			jwtv5.WithValidMethods([]string{string(m.alg)}),
			jwtv5.WithIssuer(m.issuer),
			jwtv5.WithExpirationRequired(),
			jwtv5.WithLeeway(m.leeway),
			jwtv5.WithTimeFunc(func() time.Time { return m.clock.Now().UTC() }),
		)
		tok, err := parser.Parse(tokenString, kf)
		if err != nil {
			last = err
			continue
		}
		mc, ok := tok.Claims.(jwtv5.MapClaims)
		if !ok || !tok.Valid {
			last = ErrInvalidToken
			continue
		}
		claims := claimsFromMap(mc)
		if err := matchAudience(claims.Audience, m.audience); err != nil {
			last = err
			continue
		}
		return claims, nil
	}
	var ve *ValidationError
	if stderrors.As(last, &ve) {
		return Claims{}, last
	}
	return Claims{}, mapParseError(last)
}

// matchAudience 要求 Token aud 与期望列表有交集（三端隔离：调用方传入本端 audience）。
func matchAudience(tokenAud, expected []string) error {
	if len(expected) == 0 {
		return validationErr(ErrInvalidAudience, "expected audience empty")
	}
	if len(tokenAud) == 0 {
		return validationErr(ErrInvalidAudience, "token missing aud")
	}
	set := make(map[string]struct{}, len(expected))
	for _, e := range expected {
		set[e] = struct{}{}
	}
	for _, a := range tokenAud {
		if _, ok := set[a]; ok {
			return nil
		}
	}
	return validationErr(ErrInvalidAudience, "audience mismatch")
}

func mapParseError(err error) error {
	if err == nil {
		return ErrInvalidToken
	}
	switch {
	case stderrors.Is(err, jwtv5.ErrTokenExpired):
		return validationErr(ErrExpired, "")
	case stderrors.Is(err, jwtv5.ErrTokenNotValidYet):
		return validationErr(ErrNotYetValid, "")
	case stderrors.Is(err, jwtv5.ErrTokenInvalidIssuer):
		return validationErr(ErrInvalidIssuer, "")
	case stderrors.Is(err, jwtv5.ErrTokenInvalidAudience):
		return validationErr(ErrInvalidAudience, "")
	default:
		return validationErr(ErrInvalidToken, err.Error())
	}
}

package jwt

import (
	"crypto/rsa"
	stderrors "errors"
	"fmt"
	"strings"
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
	// keyID 是签发时写入 header 的 kid；空值表示旧版单密钥模式（不写 kid）。
	keyID string
	// 密钥环：kid → 密钥材料；启用后校验按 kid 精确选键，未知 kid 直接失败。
	hmacKeyRing  map[string][]byte
	rsaPrivRing  map[string]*rsa.PrivateKey
	rsaPubRing   map[string]*rsa.PublicKey
	keyRingInUse bool
}

// HMACKey 是 HMAC 密钥环中的一项。
type HMACKey struct {
	// KID 密钥标识，签发写入 header kid，校验按此选取。
	KID string
	// Secret 对称密钥；长度须 ≥32 字节。
	Secret []byte
}

// RSAKey 是 RSA 密钥环中的一项。
type RSAKey struct {
	// KID 密钥标识。
	KID string
	// PrivateKey 签发使用；仅校验时可为空。
	PrivateKey *rsa.PrivateKey
	// PublicKey 校验使用；为空且 PrivateKey 非空时取其公钥。
	PublicKey *rsa.PublicKey
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
	// KeyID 签发写入 header 的 kid；使用 Keys 密钥环时必填。
	KeyID string
	// Keys 密钥环；非空时按 kid 精确签发 / 校验，Secret / Secrets 不再生效。
	Keys []HMACKey
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
	// KeyID 签发写入 header 的 kid；使用 Keys 密钥环时必填。
	KeyID string
	// Keys 密钥环；非空时按 kid 精确签发 / 校验，其余密钥字段不再生效。
	Keys []RSAKey
}

// NewHMAC 创建 HS256 Manager。
func NewHMAC(cfg HMACConfig) (*Manager, error) {
	if err := validateCommon(cfg.Issuer, cfg.Audience, cfg.TTL); err != nil {
		return nil, err
	}
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock()
	}
	if len(cfg.Keys) > 0 {
		return newHMACKeyRing(cfg, clock)
	}
	if len(cfg.Secret) == 0 {
		return nil, ErrMissingSecret
	}
	if len(cfg.Secret) < 32 {
		return nil, fmt.Errorf("%w: secret should be at least 32 bytes", ErrMissingSecret)
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

// newHMACKeyRing 构造带 kid 的 HS256 管理器。
func newHMACKeyRing(cfg HMACConfig, clock Clock) (*Manager, error) {
	ring := make(map[string][]byte, len(cfg.Keys))
	for _, key := range cfg.Keys {
		kid := strings.TrimSpace(key.KID)
		if kid == "" {
			return nil, validationErr(ErrUnknownKeyID, "hmac key id is required")
		}
		if len(key.Secret) < 32 {
			return nil, fmt.Errorf("%w: hmac key %q should be at least 32 bytes", ErrMissingSecret, kid)
		}
		if _, dup := ring[kid]; dup {
			return nil, validationErr(ErrUnknownKeyID, "duplicate hmac key id: "+kid)
		}
		ring[kid] = append([]byte(nil), key.Secret...)
	}
	keyID := strings.TrimSpace(cfg.KeyID)
	active, ok := ring[keyID]
	if keyID == "" || !ok {
		return nil, validationErr(ErrUnknownKeyID, "active key id is not in the key ring")
	}
	return &Manager{
		alg:          AlgorithmHS256,
		issuer:       cfg.Issuer,
		audience:     append([]string(nil), cfg.Audience...),
		ttl:          cfg.TTL,
		leeway:       cfg.Leeway,
		clock:        clock,
		hmacKey:      active,
		keyID:        keyID,
		hmacKeyRing:  ring,
		keyRingInUse: true,
	}, nil
}

// NewRSA 创建 RS256 Manager。
func NewRSA(cfg RSAConfig) (*Manager, error) {
	if err := validateCommon(cfg.Issuer, cfg.Audience, cfg.TTL); err != nil {
		return nil, err
	}
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock()
	}
	if len(cfg.Keys) > 0 {
		return newRSAKeyRing(cfg, clock)
	}
	pub := cfg.PublicKey
	if pub == nil && cfg.PrivateKey != nil {
		pub = &cfg.PrivateKey.PublicKey
	}
	if pub == nil {
		return nil, ErrMissingKey
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

// newRSAKeyRing 构造带 kid 的 RS256 管理器。
func newRSAKeyRing(cfg RSAConfig, clock Clock) (*Manager, error) {
	privRing := make(map[string]*rsa.PrivateKey, len(cfg.Keys))
	pubRing := make(map[string]*rsa.PublicKey, len(cfg.Keys))
	for _, key := range cfg.Keys {
		kid := strings.TrimSpace(key.KID)
		if kid == "" {
			return nil, validationErr(ErrUnknownKeyID, "rsa key id is required")
		}
		if _, dup := pubRing[kid]; dup {
			return nil, validationErr(ErrUnknownKeyID, "duplicate rsa key id: "+kid)
		}
		pub := key.PublicKey
		if pub == nil && key.PrivateKey != nil {
			pub = &key.PrivateKey.PublicKey
		}
		if pub == nil {
			return nil, fmt.Errorf("%w: rsa key %q has no public key", ErrMissingKey, kid)
		}
		pubRing[kid] = pub
		if key.PrivateKey != nil {
			privRing[kid] = key.PrivateKey
		}
	}
	keyID := strings.TrimSpace(cfg.KeyID)
	pub, ok := pubRing[keyID]
	if keyID == "" || !ok {
		return nil, validationErr(ErrUnknownKeyID, "active key id is not in the key ring")
	}
	return &Manager{
		alg:          AlgorithmRS256,
		issuer:       cfg.Issuer,
		audience:     append([]string(nil), cfg.Audience...),
		ttl:          cfg.TTL,
		leeway:       cfg.Leeway,
		clock:        clock,
		rsaPriv:      privRing[keyID],
		rsaPub:       pub,
		keyID:        keyID,
		rsaPrivRing:  privRing,
		rsaPubRing:   pubRing,
		keyRingInUse: true,
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
		m.setKeyIDHeader(tok)
		return tok.SignedString(m.hmacKey)
	case AlgorithmRS256:
		if m.rsaPriv == nil {
			return "", ErrMissingKey
		}
		tok := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, mc)
		m.setKeyIDHeader(tok)
		return tok.SignedString(m.rsaPriv)
	default:
		return "", ErrUnsupportedAlgorithm
	}
}

// setKeyIDHeader 在签发 header 写入 kid；旧版单密钥模式不写，保持兼容。
func (m *Manager) setKeyIDHeader(tok *jwtv5.Token) {
	if m.keyID != "" {
		tok.Header["kid"] = m.keyID
	}
}

// KeyID 返回签发使用的 kid；旧版单密钥模式为空。
func (m *Manager) KeyID() string {
	if m == nil {
		return ""
	}
	return m.keyID
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
		if m.keyRingInUse {
			return m.parseWithKeys(tokenString, []jwtv5.Keyfunc{m.keyRingKeyfunc(string(AlgorithmHS256))})
		}
		return m.parseWithKeys(tokenString, m.hmacKeyFuncs())
	case AlgorithmRS256:
		if m.keyRingInUse {
			return m.parseWithKeys(tokenString, []jwtv5.Keyfunc{m.keyRingKeyfunc(string(AlgorithmRS256))})
		}
		return m.parseWithKeys(tokenString, m.rsaKeyFuncs())
	default:
		return zero, ErrUnsupportedAlgorithm
	}
}

// keyRingKeyfunc 按 header kid 精确选取密钥；缺失或未知 kid 直接失败。
func (m *Manager) keyRingKeyfunc(alg string) jwtv5.Keyfunc {
	return func(t *jwtv5.Token) (any, error) {
		if t.Method.Alg() != alg {
			return nil, ErrUnsupportedAlgorithm
		}
		kid := tokenKeyID(t)
		if kid == "" {
			return nil, validationErr(ErrUnknownKeyID, "token missing kid header")
		}
		if alg == string(AlgorithmHS256) {
			key, ok := m.hmacKeyRing[kid]
			if !ok {
				return nil, validationErr(ErrUnknownKeyID, "unknown kid: "+kid)
			}
			return key, nil
		}
		key, ok := m.rsaPubRing[kid]
		if !ok {
			return nil, validationErr(ErrUnknownKeyID, "unknown kid: "+kid)
		}
		return key, nil
	}
}

// tokenKeyID 读取 header kid；非字符串或空值返回空串。
func tokenKeyID(t *jwtv5.Token) string {
	if t == nil || t.Header == nil {
		return ""
	}
	v, ok := t.Header["kid"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
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

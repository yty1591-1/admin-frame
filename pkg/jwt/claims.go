package jwt

import (
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// Claims 是本包使用的声明模型（标准字段 + 可扩展 Extra）。
//
// Extra 可放 merchant_id、roles 等，但本包不解释其业务语义；
// 调用方负责与端别策略对齐（SEC-03）。
type Claims struct {
	// Subject 对应 sub（主体 ID）。
	Subject string
	// Issuer 对应 iss；Sign 时若为空则使用 Manager 配置的 Issuer。
	Issuer string
	// Audience 对应 aud；Sign 时若为空则使用 Manager 配置的 Audience。
	Audience []string
	// ExpiresAt 对应 exp；零值时 Sign 按 TTL 计算。
	ExpiresAt time.Time
	// NotBefore 对应 nbf；零值表示不设置。
	NotBefore time.Time
	// IssuedAt 对应 iat；零值时 Sign 使用当前时间。
	IssuedAt time.Time
	// ID 对应 jti（可选，用于吊销/去重）。
	ID string
	// Extra 自定义声明；键不得覆盖标准注册名（sub/iss/aud/exp/nbf/iat/jti）。
	Extra map[string]any
}

// GetString 从 Extra 读取字符串声明；不存在或类型不匹配时返回 ""。
func (c Claims) GetString(key string) string {
	if c.Extra == nil {
		return ""
	}
	v, ok := c.Extra[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// GetStringSlice 从 Extra 读取字符串切片（或单个 string 升为单元素切片）。
func (c Claims) GetStringSlice(key string) []string {
	if c.Extra == nil {
		return nil
	}
	v, ok := c.Extra[key]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (c Claims) toMap(now time.Time, defaultISS string, defaultAUD []string, ttl time.Duration) (jwtv5.MapClaims, error) {
	mc := jwtv5.MapClaims{}
	for k, v := range c.Extra {
		if isRegisteredClaim(k) {
			return nil, validationErr(ErrInvalidToken, "extra overrides registered claim: "+k)
		}
		mc[k] = v
	}

	sub := c.Subject
	if sub != "" {
		mc["sub"] = sub
	}
	iss := c.Issuer
	if iss == "" {
		iss = defaultISS
	}
	if iss != "" {
		mc["iss"] = iss
	}
	aud := c.Audience
	if len(aud) == 0 {
		aud = append([]string(nil), defaultAUD...)
	}
	if len(aud) == 1 {
		mc["aud"] = aud[0]
	} else if len(aud) > 1 {
		mc["aud"] = aud
	}

	iat := c.IssuedAt
	if iat.IsZero() {
		iat = now
	}
	mc["iat"] = iat.UTC().Unix()

	exp := c.ExpiresAt
	if exp.IsZero() {
		if ttl <= 0 {
			return nil, validationErr(ErrInvalidToken, "ExpiresAt or TTL required")
		}
		exp = iat.Add(ttl)
	}
	mc["exp"] = exp.UTC().Unix()

	if !c.NotBefore.IsZero() {
		mc["nbf"] = c.NotBefore.UTC().Unix()
	}
	if c.ID != "" {
		mc["jti"] = c.ID
	}
	return mc, nil
}

func claimsFromMap(mc jwtv5.MapClaims) Claims {
	c := Claims{Extra: map[string]any{}}
	if v, err := mc.GetSubject(); err == nil {
		c.Subject = v
	}
	if v, err := mc.GetIssuer(); err == nil {
		c.Issuer = v
	}
	if v, err := mc.GetAudience(); err == nil {
		c.Audience = []string(v)
	}
	if v, err := mc.GetExpirationTime(); err == nil && v != nil {
		c.ExpiresAt = v.Time
	}
	if v, err := mc.GetNotBefore(); err == nil && v != nil {
		c.NotBefore = v.Time
	}
	if v, err := mc.GetIssuedAt(); err == nil && v != nil {
		c.IssuedAt = v.Time
	}
	if v, ok := mc["jti"].(string); ok {
		c.ID = v
	}
	for k, v := range mc {
		if isRegisteredClaim(k) {
			continue
		}
		c.Extra[k] = v
	}
	if len(c.Extra) == 0 {
		c.Extra = nil
	}
	return c
}

func isRegisteredClaim(k string) bool {
	switch k {
	case "sub", "iss", "aud", "exp", "nbf", "iat", "jti":
		return true
	default:
		return false
	}
}

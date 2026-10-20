package webhook

import (
	"bytes"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const (
	// HeaderID 是默认的消息 ID 头名。
	HeaderID = "webhook-id"
	// HeaderTimestamp 是默认的 Unix 秒级时间戳头名。
	HeaderTimestamp = "webhook-timestamp"
	// HeaderSignature 是默认的签名头名。
	HeaderSignature = "webhook-signature"

	// DefaultTolerance 是默认允许的时间戳偏移（过去与未来各 5 分钟）。
	DefaultTolerance = 5 * time.Minute

	// DefaultMaxBodyBytes 是默认请求体上限（1 MiB）。
	DefaultMaxBodyBytes int64 = 1 << 20

	// DefaultVersion 是默认签名版本标识。
	DefaultVersion = "v1"

	// MinKeyBytes 是单个密钥的最小长度。
	//
	// HMAC 密钥短于摘要长度会削弱安全性；建议使用 32 字节以上的随机值。
	MinKeyBytes = 32
)

const (
	maxTolerance    = 24 * time.Hour
	maxBodyBytes    = 8 << 20
	maxKeyBytes     = 4096
	maxKeys         = 8
	maxVersionBytes = 16
)

// HeaderNames 是签名相关 HTTP 头名。
//
// 零值字段在 Normalized 中补默认值；自定义时三个名称必须合法且互不重复
// （大小写不敏感）。
type HeaderNames struct {
	// ID 携带消息唯一 ID，用于重放键。
	ID string
	// Timestamp 携带 Unix 秒级时间戳。
	Timestamp string
	// Signature 携带 <version>,<base64> 签名；多个签名以空格分隔。
	Signature string
}

// DefaultHeaderNames 返回 Standard Webhooks 约定的头名。
func DefaultHeaderNames() HeaderNames {
	return HeaderNames{ID: HeaderID, Timestamp: HeaderTimestamp, Signature: HeaderSignature}
}

// Config 是 Verifier / Signer 的统一配置。
//
// 零值不可直接使用：必须至少提供 Keys，由 NewVerifier / NewSigner 完成校验。
// 本结构不读取文件或环境变量；YAML/env 映射由 internal/platform 完成（CFG-07）。
// 密钥属于敏感信息，禁止写入 YAML 明文或镜像层（SEC-01）。
type Config struct {
	// Keys 是共享密钥：Verifier 接受任一匹配，Signer 用全部密钥签名。
	//
	// 至少 1 个、最多 8 个，每个 32～4096 字节且互不相同。
	// 多密钥用于平滑轮换：先加入新密钥，再移除旧密钥。
	Keys [][]byte

	// Algorithm 是签名算法；nil 时使用 HMAC-SHA256。
	Algorithm Algorithm

	// Version 是签名版本标识；空则 "v1"，限 [A-Za-z0-9._-]{1,16}。
	Version string

	// Headers 是签名头名；零值字段补默认值。
	Headers HeaderNames

	// Tolerance 是允许的时间戳偏移，仅 Verifier 使用；
	// 0 则 5m，必须 ≤ 24h。
	Tolerance time.Duration

	// MaxBodyBytes 是请求体上限，Verifier 与 Signer 都使用；
	// 0 则 1 MiB，必须 ≤ 8 MiB。
	MaxBodyBytes int64

	// Nonce 是重放防护存储，仅 Verifier 使用且必填。
	//
	// 多实例部署必须注入共享实现（如 Redis SET NX EX）；MemoryNonceStore
	// 只覆盖单进程，不能防止跨实例重放。同一 Store 应仅服务一个发送方。
	Nonce NonceStore

	// Clock 是注入时钟；nil 时使用系统时钟。
	Clock clock.Clock

	// Observer 接收校验事件；可为 nil。
	Observer Observer

	// OnError 定制 Verifier.Middleware 的错误响应；nil 时按哨兵写状态码与通用文案。
	//
	// 实现不得把 err 原文直接返回客户端（可能含头名等内部细节）。
	OnError func(w http.ResponseWriter, r *http.Request, err error)
}

// Normalized 返回补齐默认值并深拷贝密钥后的副本；不修改接收者。
func (c Config) Normalized() Config {
	if c.Algorithm == nil {
		c.Algorithm = HMACSHA256()
	}
	if c.Version == "" {
		c.Version = DefaultVersion
	}
	c.Headers = c.Headers.normalized()
	if c.Tolerance <= 0 {
		c.Tolerance = DefaultTolerance
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.Clock == nil {
		c.Clock = clock.RealClock()
	}
	if len(c.Keys) > 0 {
		keys := make([][]byte, len(c.Keys))
		for i, key := range c.Keys {
			keys[i] = append([]byte(nil), key...)
		}
		c.Keys = keys
	}
	return c
}

// Validate 校验通用字段：密钥、版本与头名。
//
// Verifier 还要求 Tolerance、MaxBodyBytes 与 Nonce 合法，完整校验由
// NewVerifier 执行；Signer 由 NewSigner 执行。二者都会先调用本方法。
func (c Config) Validate() error {
	c = c.Normalized()
	if len(c.Keys) == 0 {
		return invalidConfig("at least one key is required")
	}
	if len(c.Keys) > maxKeys {
		return invalidConfig("at most %d keys are allowed", maxKeys)
	}
	for i, key := range c.Keys {
		if len(key) < MinKeyBytes {
			return invalidConfig("key %d must be at least %d bytes", i, MinKeyBytes)
		}
		if len(key) > maxKeyBytes {
			return invalidConfig("key %d must be at most %d bytes", i, maxKeyBytes)
		}
		for j := range i {
			if bytes.Equal(key, c.Keys[j]) {
				return invalidConfig("key %d duplicates key %d", i, j)
			}
		}
	}
	if !validVersion(c.Version) {
		return invalidConfig("version must match [A-Za-z0-9._-]{1,%d}", maxVersionBytes)
	}
	if err := c.Headers.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Algorithm.Name()) == "" {
		return invalidConfig("algorithm name must not be empty")
	}
	return nil
}

// validateVerifier 校验 Verifier 专属字段。
func (c Config) validateVerifier() error {
	if c.Tolerance <= 0 || c.Tolerance > maxTolerance {
		return invalidConfig("tolerance must be in (0,%s]", maxTolerance)
	}
	if c.MaxBodyBytes <= 0 || c.MaxBodyBytes > maxBodyBytes {
		return invalidConfig("max body bytes must be in (0,%d]", maxBodyBytes)
	}
	if c.Nonce == nil {
		return invalidConfig("nonce store is required")
	}
	return nil
}

func (h HeaderNames) normalized() HeaderNames {
	defaults := DefaultHeaderNames()
	if h.ID == "" {
		h.ID = defaults.ID
	}
	if h.Timestamp == "" {
		h.Timestamp = defaults.Timestamp
	}
	if h.Signature == "" {
		h.Signature = defaults.Signature
	}
	return h
}

func (h HeaderNames) validate() error {
	names := [...]struct {
		field string
		value string
	}{
		{field: "ID", value: h.ID},
		{field: "Timestamp", value: h.Timestamp},
		{field: "Signature", value: h.Signature},
	}
	seen := make(map[string]string, len(names))
	for _, item := range names {
		if !validHeaderName(item.value) {
			return invalidConfig("header name %s is not a valid HTTP token", item.field)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(item.value)
		if previous, ok := seen[canonical]; ok {
			return invalidConfig("header names %s and %s collide", previous, item.field)
		}
		seen[canonical] = item.field
	}
	return nil
}

// validHeaderName 按 RFC 9110 token 规则校验头名。
func validHeaderName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '!', c == '#', c == '$', c == '%', c == '&', c == '\'',
		c == '*', c == '+', c == '-', c == '.', c == '^', c == '_',
		c == '`', c == '|', c == '~':
		return true
	default:
		return false
	}
}

func validVersion(version string) bool {
	if version == "" || len(version) > maxVersionBytes {
		return false
	}
	for i := 0; i < len(version); i++ {
		c := version[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

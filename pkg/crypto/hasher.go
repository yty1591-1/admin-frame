package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// digestSeparator 分隔 key id 与摘要。
const digestSeparator = ":"

// Hasher 是带密钥的 HMAC-SHA256 摘要器，用于 PII 与低熵验证码（IDN-P1-06）。
//
// 输出格式为 `<key_id>:<hex>`；带 key id 便于轮换与双读。输入统一为
// `HMAC-SHA256(key, domain || "|" || value)`，domain 之间不可交叉复用。
type Hasher struct {
	active string
	keys   map[string][]byte
}

// NewHasher 构造摘要器；active 必须存在，keys 中每个密钥须为 32 字节。
func NewHasher(active string, keys map[string][]byte) (*Hasher, error) {
	if strings.TrimSpace(active) == "" {
		return nil, fmt.Errorf("%w: hash key id is required", ErrInvalidKey)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: hash key is required", ErrInvalidKey)
	}
	normalized := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%w: hash key id must not be empty", ErrInvalidKey)
		}
		if len(key) != AES256KeySize {
			return nil, fmt.Errorf("%w: hash key %q must be %d bytes", ErrInvalidKey, id, AES256KeySize)
		}
		cp := make([]byte, len(key))
		copy(cp, key)
		normalized[id] = cp
	}
	if _, ok := normalized[active]; !ok {
		return nil, fmt.Errorf("%w: active hash key %q is not configured", ErrInvalidKey, active)
	}
	return &Hasher{active: active, keys: normalized}, nil
}

// ActiveKeyID 返回当前用于写路径的 key id。
func (h *Hasher) ActiveKeyID() string {
	if h == nil {
		return ""
	}
	return h.active
}

// Digest 使用 active 密钥生成摘要；nil 摘要器返回空串。
func (h *Hasher) Digest(domain, value string) string {
	if h == nil {
		return ""
	}
	key := h.keys[h.active]
	return h.active + digestSeparator + sumHex(key, domain, value)
}

// Digests 返回 active 与全部历史密钥的摘要，供双读消费（如恢复码轮换）。
func (h *Hasher) Digests(domain, value string) []string {
	if h == nil {
		return nil
	}
	out := make([]string, 0, len(h.keys))
	out = append(out, h.Digest(domain, value))
	for id, key := range h.keys {
		if id == h.active {
			continue
		}
		out = append(out, id+digestSeparator+sumHex(key, domain, value))
	}
	return out
}

// Verify 校验带 key id 的摘要；未知 key id 或格式非法返回 false。
func (h *Hasher) Verify(digest, domain, value string) bool {
	if h == nil {
		return false
	}
	id, encoded, ok := strings.Cut(strings.TrimSpace(digest), digestSeparator)
	if !ok || id == "" || encoded == "" {
		return false
	}
	key, exists := h.keys[id]
	if !exists {
		return false
	}
	expected := sumHex(key, domain, value)
	return subtle.ConstantTimeCompare([]byte(encoded), []byte(expected)) == 1
}

// KeyID 返回摘要使用的 key id；无前缀返回空串。
func (h *Hasher) KeyID(digest string) string {
	id, _, ok := strings.Cut(strings.TrimSpace(digest), digestSeparator)
	if !ok {
		return ""
	}
	return id
}

// sumHex 计算 HMAC-SHA256(key, domain|value) 的十六进制摘要。
func sumHex(key []byte, domain, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domain))
	mac.Write([]byte("|"))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

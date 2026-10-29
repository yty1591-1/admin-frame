package crypto

import "fmt"

// keyRingBlobVersion 是密钥环密文的版本前缀，与单密钥 AEAD 的 0x01 区分。
const keyRingBlobVersion byte = 0x02

// maxKeyIDLen 是 key id 的最大字节数，写入密文头。
const maxKeyIDLen = 16

// KeyRing 是按 key id 选择密钥的 AES-256-GCM 环，用于 MFA 密钥轮换（IDN-P1-05）。
//
// 新密文格式：0x02 || kidLen || kid || AEAD 密文。
// 解密同时兼容旧格式（0x01 || nonce || ciphertext），由 legacy 密钥处理。
type KeyRing struct {
	keys   map[string]*AEAD
	active string
	legacy *AEAD
}

// NewKeyRing 构造密钥环；active 必须存在于 keys 中，keys 为空时返回错误。
//
// legacyKey 可为 nil：此时不再解密旧格式密文（仅用于已完成迁移部署）。
func NewKeyRing(keys map[string][]byte, active string, legacyKey []byte) (*KeyRing, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: key ring requires at least one key", ErrInvalidKey)
	}
	if active == "" {
		return nil, fmt.Errorf("%w: active key id is required", ErrInvalidKey)
	}
	ring := &KeyRing{keys: make(map[string]*AEAD, len(keys)), active: active}
	for id, key := range keys {
		if id == "" {
			return nil, fmt.Errorf("%w: key id must not be empty", ErrInvalidKey)
		}
		if len(id) > maxKeyIDLen {
			return nil, fmt.Errorf("%w: key id exceeds %d bytes", ErrInvalidKey, maxKeyIDLen)
		}
		aead, err := NewAES256GCM(key)
		if err != nil {
			ring.Close()
			return nil, err
		}
		ring.keys[id] = aead
	}
	if _, ok := ring.keys[active]; !ok {
		ring.Close()
		return nil, fmt.Errorf("%w: active key id %q is not configured", ErrInvalidKey, active)
	}
	if len(legacyKey) > 0 {
		aead, err := NewAES256GCM(legacyKey)
		if err != nil {
			ring.Close()
			return nil, err
		}
		ring.legacy = aead
	}
	return ring, nil
}

// ActiveKeyID 返回当前用于加密的 key id。
func (r *KeyRing) ActiveKeyID() string {
	if r == nil {
		return ""
	}
	return r.active
}

// KeyID 返回密文使用的 key id；旧格式返回空串。
func (r *KeyRing) KeyID(blob []byte) string {
	if len(blob) < 2 || blob[0] != keyRingBlobVersion {
		return ""
	}
	length := int(blob[1])
	if length == 0 || length > maxKeyIDLen || len(blob) < 2+length {
		return ""
	}
	return string(blob[2 : 2+length])
}

// Seal 使用 active 密钥加密，并写入 key id。
func (r *KeyRing) Seal(plaintext, aad []byte) ([]byte, error) {
	if r == nil || len(r.keys) == 0 {
		return nil, ErrInvalidKey
	}
	aead, ok := r.keys[r.active]
	if !ok || aead == nil {
		return nil, ErrInvalidKey
	}
	inner, err := aead.Seal(plaintext, aad)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 2+len(r.active)+len(inner))
	out = append(out, keyRingBlobVersion, byte(len(r.active)))
	out = append(out, r.active...)
	out = append(out, inner...)
	return out, nil
}

// Open 按密文头的 key id 解密；旧格式回退 legacy 密钥。
func (r *KeyRing) Open(blob, aad []byte) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidKey
	}
	if len(blob) > 0 && blob[0] == keyRingBlobVersion {
		if len(blob) < 2 {
			return nil, ErrInvalidCiphertext
		}
		length := int(blob[1])
		if length == 0 || length > maxKeyIDLen || len(blob) < 2+length {
			return nil, ErrInvalidCiphertext
		}
		kid := string(blob[2 : 2+length])
		aead, ok := r.keys[kid]
		if !ok || aead == nil {
			return nil, ErrInvalidCiphertext
		}
		return aead.Open(blob[2+length:], aad)
	}
	if r.legacy == nil {
		return nil, ErrInvalidCiphertext
	}
	return r.legacy.Open(blob, aad)
}

// NeedsRotation 报告密文是否应重加密为 active key（旧格式或非 active key）。
func (r *KeyRing) NeedsRotation(blob []byte) bool {
	if r == nil {
		return false
	}
	return r.KeyID(blob) != r.active
}

// Close 清零全部密钥副本。
func (r *KeyRing) Close() {
	if r == nil {
		return
	}
	for _, aead := range r.keys {
		aead.Close()
	}
	if r.legacy != nil {
		r.legacy.Close()
	}
	r.keys = nil
	r.legacy = nil
}

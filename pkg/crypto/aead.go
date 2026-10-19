package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// AEAD 是 AES-256-GCM 封装；密钥在构造时拷贝，调用方可立即 ClearBytes 原切片。
//
// AEAD 实例可并发 Seal/Open；内部无可变状态。
type AEAD struct {
	gcm cipher.AEAD
	key []byte // 保留副本便于 Close 清零
}

// NewAES256GCM 使用 32 字节密钥创建 AES-256-GCM。
func NewAES256GCM(key []byte) (*AEAD, error) {
	if len(key) != AES256KeySize {
		return nil, fmt.Errorf("%w: aes-256 key must be %d bytes", ErrInvalidKey, AES256KeySize)
	}
	keyCopy := make([]byte, AES256KeySize)
	copy(keyCopy, key)

	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		ClearBytes(keyCopy)
		return nil, fmt.Errorf("crypto: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		ClearBytes(keyCopy)
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	if gcm.NonceSize() != NonceSize {
		ClearBytes(keyCopy)
		return nil, fmt.Errorf("crypto: unexpected nonce size %d", gcm.NonceSize())
	}
	return &AEAD{gcm: gcm, key: keyCopy}, nil
}

// Seal 加密 plaintext；aad 为附加认证数据（可 nil，参与认证但不加密）。
// 返回 version||nonce||ciphertext；不得将返回值当作密钥日志。
func (a *AEAD) Seal(plaintext, aad []byte) ([]byte, error) {
	if a == nil || a.gcm == nil {
		return nil, ErrInvalidKey
	}
	nonce, err := RandomBytes(a.gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+a.gcm.Overhead())
	out[0] = blobVersion
	copy(out[1:], nonce)
	out = a.gcm.Seal(out, nonce, plaintext, aad)
	return out, nil
}

// Open 解密 Seal 产物；aad 须与加密时一致。
// 认证失败统一返回 ErrInvalidCiphertext（不泄露明文或密钥）。
func (a *AEAD) Open(blob, aad []byte) ([]byte, error) {
	if a == nil || a.gcm == nil {
		return nil, ErrInvalidKey
	}
	ns := a.gcm.NonceSize()
	minLen := 1 + ns + a.gcm.Overhead()
	if len(blob) < minLen {
		return nil, ErrInvalidCiphertext
	}
	if blob[0] != blobVersion {
		return nil, ErrInvalidCiphertext
	}
	nonce := blob[1 : 1+ns]
	ct := blob[1+ns:]
	pt, err := a.gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return pt, nil
}

// Close 清零内部密钥副本；之后 Seal/Open 不可用。
func (a *AEAD) Close() {
	if a == nil {
		return
	}
	ClearBytes(a.key)
	a.gcm = nil
	a.key = nil
}

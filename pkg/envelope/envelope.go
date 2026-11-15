// Package envelope 提供业务密钥信封加密（ADR-0014）。
//
// AES-256-GCM + 每版本独立 DEK；DEK 由 KEKProvider wrap / unwrap。
// AAD = env:namespace:name:version，防止密文跨上下文搬运。
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
)

const (
	// Algorithm 是固定信封算法标识。
	Algorithm = "aes-256-gcm"
	// NonceSize 是 GCM 标准 nonce 长度。
	NonceSize = 12
	// DEKSize 是数据加密密钥长度（32 字节 = AES-256）。
	DEKSize = 32
)

// KEKProvider 是外部根密钥包装接口；admin-api 仅 Wrap，config-api 仅 Unwrap。
type KEKProvider interface {
	Wrap(dek []byte) (wrapped []byte, keyID string, err error)
	Unwrap(wrapped []byte, keyID string) (dek []byte, err error)
	KeyID() string
}

// Blob 是入库的信封载荷。
type Blob struct {
	Ciphertext []byte
	Nonce      []byte
	WrappedDEK []byte
	KEKKeyID   string
	Algorithm  string
	ValueHash  string
}

// AAD 构造认证附加数据。
func AAD(env, namespace, name string, version int64) []byte {
	return []byte(env + ":" + namespace + ":" + name + ":" + strconv.FormatInt(version, 10))
}

// Fingerprint 返回 SHA-256(plaintext) 前 8 字节 hex（16 字符）。
func Fingerprint(plaintext []byte) string {
	sum := sha256.Sum256(plaintext)
	return hex.EncodeToString(sum[:8])
}

// Seal 生成 DEK、加密明文并用 KEK wrap DEK。
func Seal(kek KEKProvider, plaintext []byte, aad []byte) (Blob, error) {
	if kek == nil {
		return Blob{}, fmt.Errorf("envelope: kek provider required")
	}
	if len(plaintext) == 0 {
		return Blob{}, fmt.Errorf("envelope: plaintext required")
	}
	dek := make([]byte, DEKSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return Blob{}, fmt.Errorf("envelope: generate dek: %w", err)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return Blob{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Blob{}, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Blob{}, fmt.Errorf("envelope: generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	wrapped, keyID, err := kek.Wrap(dek)
	if err != nil {
		return Blob{}, fmt.Errorf("envelope: wrap dek: %w", err)
	}
	return Blob{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		WrappedDEK: wrapped,
		KEKKeyID:   keyID,
		Algorithm:  Algorithm,
		ValueHash:  Fingerprint(plaintext),
	}, nil
}

// Open 用 KEK unwrap DEK 后解密；AAD 不匹配或指纹不符则失败。
func Open(kek KEKProvider, blob Blob, aad []byte) ([]byte, error) {
	if kek == nil {
		return nil, fmt.Errorf("envelope: kek provider required")
	}
	if blob.Algorithm != "" && blob.Algorithm != Algorithm {
		return nil, fmt.Errorf("envelope: unsupported algorithm %q", blob.Algorithm)
	}
	if len(blob.Nonce) != NonceSize {
		return nil, fmt.Errorf("envelope: invalid nonce size")
	}
	dek, err := kek.Unwrap(blob.WrappedDEK, blob.KEKKeyID)
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap dek: %w", err)
	}
	if len(dek) != DEKSize {
		return nil, fmt.Errorf("envelope: invalid dek size")
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, blob.Nonce, blob.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("envelope: decrypt: %w", err)
	}
	if blob.ValueHash != "" && Fingerprint(plaintext) != blob.ValueHash {
		return nil, fmt.Errorf("envelope: fingerprint mismatch")
	}
	return plaintext, nil
}

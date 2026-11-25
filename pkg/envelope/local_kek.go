package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// LocalKEK 用进程内对称根密钥 wrap DEK；仅允许 non-prod。
//
// 根密钥来源：构造参数或环境变量 CONFIGCENTER_LOCAL_KEK_HEX（64 hex = 32 bytes）。
type LocalKEK struct {
	key   []byte
	keyID string
}

// NewLocalKEK 从 32 字节根密钥构造；keyID 默认 local/1。
func NewLocalKEK(root []byte, keyID string) (*LocalKEK, error) {
	if len(root) != 32 {
		return nil, fmt.Errorf("envelope: local kek must be 32 bytes")
	}
	if strings.TrimSpace(keyID) == "" {
		keyID = "local/1"
	}
	cloned := make([]byte, 32)
	copy(cloned, root)
	return &LocalKEK{key: cloned, keyID: keyID}, nil
}

// NewLocalKEKFromEnv 读取 CONFIGCENTER_LOCAL_KEK_HEX；缺失则随机生成（仅测试）。
func NewLocalKEKFromEnv() (*LocalKEK, error) {
	raw := strings.TrimSpace(os.Getenv("CONFIGCENTER_LOCAL_KEK_HEX"))
	if raw == "" {
		root := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, root); err != nil {
			return nil, err
		}
		return NewLocalKEK(root, "local/ephemeral")
	}
	root, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("envelope: decode CONFIGCENTER_LOCAL_KEK_HEX: %w", err)
	}
	return NewLocalKEK(root, "local/1")
}

// KeyID 返回当前 KEK 标识。
func (k *LocalKEK) KeyID() string { return k.keyID }

// Wrap 用 AES-GCM 包装 DEK；输出 = nonce || ciphertext。
func (k *LocalKEK) Wrap(dek []byte) (wrapped []byte, keyID string, err error) {
	if len(dek) != DEKSize {
		return nil, "", fmt.Errorf("envelope: dek must be %d bytes", DEKSize)
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", err
	}
	out := gcm.Seal(nonce, nonce, dek, []byte(k.keyID))
	return out, k.keyID, nil
}

// Unwrap 解包 DEK。
func (k *LocalKEK) Unwrap(wrapped []byte, keyID string) ([]byte, error) {
	if keyID != "" && keyID != k.keyID {
		return nil, fmt.Errorf("envelope: kek key_id mismatch")
	}
	if len(wrapped) < NonceSize+16 {
		return nil, fmt.Errorf("envelope: wrapped dek too short")
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, ciphertext := wrapped[:NonceSize], wrapped[NonceSize:]
	return gcm.Open(nil, nonce, ciphertext, []byte(k.keyID))
}

// RejectLocalKEKInProd 在 prod 环境禁止 LocalKEK。
func RejectLocalKEKInProd(env string, kek KEKProvider) error {
	if strings.EqualFold(env, "prod") {
		if _, ok := kek.(*LocalKEK); ok {
			return fmt.Errorf("envelope: LocalKEK is forbidden in prod")
		}
	}
	return nil
}

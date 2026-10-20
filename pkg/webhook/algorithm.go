package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
)

const (
	algorithmHMACSHA256 = "hmac-sha256"
	algorithmHMACSHA512 = "hmac-sha512"
)

// Algorithm 是 Webhook 签名算法。
//
// 实现必须并发安全：Verifier / Signer 会在多个 goroutine 中共享同一实例。
// Verify 必须使用常数时间比较；签名不匹配统一返回 ErrInvalidSignature，
// 不得区分「密钥错误」与「内容被篡改」；密钥材料非法时返回具体错误。
type Algorithm interface {
	// Name 返回稳定的小写算法名，可用于日志与配置校验（低基数）。
	Name() string
	// Sign 用 key 计算 message 的签名；key 为空返回错误。
	Sign(key, message []byte) ([]byte, error)
	// Verify 校验 signature 是否为 key 对 message 的签名。
	Verify(key, message, signature []byte) error
}

// HMACSHA256 返回 HMAC-SHA256 算法实现；无状态，可并发复用。
//
// 这是默认算法。密钥应至少 32 字节，并仅从环境变量或 KMS 注入。
func HMACSHA256() Algorithm {
	return hmacAlgorithm{name: algorithmHMACSHA256, newHash: sha256.New}
}

// HMACSHA512 返回 HMAC-SHA512 算法实现；无状态，可并发复用。
func HMACSHA512() Algorithm {
	return hmacAlgorithm{name: algorithmHMACSHA512, newHash: sha512.New}
}

type hmacAlgorithm struct {
	name    string
	newHash func() hash.Hash
}

// Name 返回算法名。
func (a hmacAlgorithm) Name() string { return a.name }

// Sign 计算 HMAC；Write 对 hash 实现不会失败，仍保留错误分支以便扩展。
func (a hmacAlgorithm) Sign(key, message []byte) (signature []byte, err error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: empty signing key", ErrInvalidConfig)
	}
	mac := hmac.New(a.newHash, key)
	if _, writeErr := mac.Write(message); writeErr != nil {
		return nil, fmt.Errorf("%w: hmac write failed", ErrInvalidConfig)
	}
	return mac.Sum(nil), nil
}

// Verify 以常数时间比较签名；长度不一致也走 hmac.Equal，不提前返回。
func (a hmacAlgorithm) Verify(key, message, signature []byte) error {
	want, err := a.Sign(key, message)
	if err != nil {
		return err
	}
	if !hmac.Equal(want, signature) {
		return ErrInvalidSignature
	}
	return nil
}

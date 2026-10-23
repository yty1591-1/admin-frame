package jwt

import (
	stderrors "errors"
	"fmt"
)

var (
	// ErrInvalidToken 表示 Token 格式非法、签名错误或声明不满足约束。
	ErrInvalidToken = stderrors.New("jwt: invalid token")
	// ErrExpired 表示 Token 已过期。
	ErrExpired = stderrors.New("jwt: token expired")
	// ErrNotYetValid 表示 Token 尚未生效（nbf）。
	ErrNotYetValid = stderrors.New("jwt: token not yet valid")
	// ErrInvalidIssuer 表示 iss 与期望不符。
	ErrInvalidIssuer = stderrors.New("jwt: invalid issuer")
	// ErrInvalidAudience 表示 aud 与期望不符。
	ErrInvalidAudience = stderrors.New("jwt: invalid audience")
	// ErrUnsupportedAlgorithm 表示算法未在允许列表中。
	ErrUnsupportedAlgorithm = stderrors.New("jwt: unsupported algorithm")
	// ErrMissingSecret 表示缺少 HMAC 密钥。
	ErrMissingSecret = stderrors.New("jwt: missing hmac secret")
	// ErrMissingKey 表示缺少 RSA 密钥。
	ErrMissingKey = stderrors.New("jwt: missing rsa key")
	// ErrUnknownKeyID 表示 Token header 的 kid 缺失或不在密钥环中。
	ErrUnknownKeyID = stderrors.New("jwt: unknown key id")
)

// ValidationError 携带可判定的校验失败原因（可 errors.Is 到上述哨兵）。
type ValidationError struct {
	// Reason 为哨兵之一（ErrExpired / ErrInvalidIssuer 等）。
	Reason error
	// Detail 仅供内部日志，不得返回给终端用户或写入审计原文。
	Detail string
}

// Error 实现 error。
func (e *ValidationError) Error() string {
	if e == nil {
		return "jwt: validation failed"
	}
	if e.Detail != "" {
		return fmt.Sprintf("%v: %s", e.Reason, e.Detail)
	}
	if e.Reason != nil {
		return e.Reason.Error()
	}
	return "jwt: validation failed"
}

// Unwrap 返回 Reason，供 errors.Is / As 使用。
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}

func validationErr(reason error, detail string) error {
	return &ValidationError{Reason: reason, Detail: detail}
}

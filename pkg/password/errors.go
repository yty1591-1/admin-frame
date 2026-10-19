package password

import stderrors "errors"

var (
	// ErrMismatch 表示密码与哈希不匹配（或应按登录失败处理）。
	ErrMismatch = stderrors.New("password: mismatch")
	// ErrInvalidHash 表示编码哈希格式非法或损坏。
	ErrInvalidHash = stderrors.New("password: invalid hash")
	// ErrEmptyPassword 表示明文密码为空。
	ErrEmptyPassword = stderrors.New("password: empty password")
	// ErrPasswordTooLong 表示明文超过算法/包上限（防 DoS）。
	ErrPasswordTooLong = stderrors.New("password: too long")
	// ErrInvalidConfig 表示算法参数不合法。
	ErrInvalidConfig = stderrors.New("password: invalid config")
	// ErrUnsupportedAlgorithm 表示无法识别的哈希前缀。
	ErrUnsupportedAlgorithm = stderrors.New("password: unsupported algorithm")
)

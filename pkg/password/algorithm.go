package password

import "strings"

// Algorithm 密码哈希算法标识。
type Algorithm string

const (
	// AlgorithmArgon2id 为默认推荐算法（内存硬化）。
	AlgorithmArgon2id Algorithm = "argon2id"
	// AlgorithmBcrypt 为广泛兼容的备选算法。
	AlgorithmBcrypt Algorithm = "bcrypt"
)

// DetectAlgorithm 根据编码哈希前缀识别算法。
func DetectAlgorithm(encoded string) (Algorithm, error) {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return AlgorithmArgon2id, nil
	case strings.HasPrefix(encoded, "$2a$"),
		strings.HasPrefix(encoded, "$2b$"),
		strings.HasPrefix(encoded, "$2y$"):
		return AlgorithmBcrypt, nil
	default:
		return "", ErrUnsupportedAlgorithm
	}
}

// MaxPasswordBytes 明文密码最大字节数（argon2id / 通用上限）。
// bcrypt 另有 72 字节有效上限，超出在 Hash/Verify 时拒绝。
const MaxPasswordBytes = 4096

// BcryptMaxBytes 是 bcrypt 规范有效输入上限。
const BcryptMaxBytes = 72

func checkPassword(plain []byte, maxBytes int) error {
	if len(plain) == 0 {
		return ErrEmptyPassword
	}
	if len(plain) > maxBytes {
		return ErrPasswordTooLong
	}
	return nil
}

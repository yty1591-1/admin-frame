package crypto

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
)

// SHA256 返回 data 的 SHA-256 摘要（32 字节）。
func SHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out
}

// SHA256Hex 返回 data 的 SHA-256 十六进制小写字符串。
func SHA256Hex(data []byte) string {
	return hex.EncodeToString(SHA256(data))
}

// SHA512 返回 data 的 SHA-512 摘要（64 字节）。
func SHA512(data []byte) []byte {
	sum := sha512.Sum512(data)
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out
}

// SHA512Hex 返回 data 的 SHA-512 十六进制小写字符串。
func SHA512Hex(data []byte) string {
	return hex.EncodeToString(SHA512(data))
}

// Equal 以常数时间比较 a 与 b 是否相等（用于 MAC / 令牌等敏感比较）。
// 长度不同时立即返回 false（仍避免对内容做短路比较）。
func Equal(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

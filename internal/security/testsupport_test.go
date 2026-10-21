package security_test

import "strings"

// testJWTSecret 返回长度恰为 32 字节的 HMAC 测试密钥。
//
// 为什么不用字面量：pkg/jwt 要求 HS256 密钥 ≥32 字节，而任何 32 位高熵字面量
// 都会被密钥扫描（gitleaks generic-api-key）识别为疑似真实凭据，导致 CI
// `make secrets` 误报。这里用重复字符构造，既满足长度约束，也表达「这是假密钥」。
//
// fill 取不同的值可得到互不相等的密钥（用于轮换场景）。
func testJWTSecret(fill byte) string {
	return strings.Repeat(string(fill), 32)
}

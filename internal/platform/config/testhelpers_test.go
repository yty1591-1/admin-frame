package config_test

import "strings"

// testJWTSecret 返回长度恰为 32 字节的 HMAC 测试密钥。
//
// 与 internal/platform 的测试 helper 语义一致：避免高熵字面量触发
// gitleaks generic-api-key 误报；fill 取不同值可得到互不相等的密钥。
func testJWTSecret(fill byte) string {
	return strings.Repeat(string(fill), 32)
}

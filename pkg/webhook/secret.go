package webhook

import (
	"strings"
)

// DecodeSecret 解析 Standard Webhooks 风格的 Base64 密钥。
//
// 接受可选的 "whsec_" 前缀、标准或未填充 Base64；返回值可直接放入
// Config.Keys。密钥必须解码到 32～4096 字节。
//
// 本函数不会把明文当作密钥，避免同一配置在不同环境下语义漂移；
// 非 Base64 输入返回包装 ErrInvalidConfig 的错误。
func DecodeSecret(secret string) ([]byte, error) {
	value := strings.TrimSpace(secret)
	value = strings.TrimPrefix(value, "whsec_")
	if value == "" {
		return nil, invalidConfig("secret is empty")
	}
	decoded, err := decodeSignature(value)
	if err != nil {
		return nil, invalidConfig("secret must be base64 encoded")
	}
	if len(decoded) < MinKeyBytes {
		return nil, invalidConfig("secret must decode to at least %d bytes", MinKeyBytes)
	}
	if len(decoded) > maxKeyBytes {
		return nil, invalidConfig("secret must decode to at most %d bytes", maxKeyBytes)
	}
	return decoded, nil
}

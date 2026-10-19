package redis

import (
	"strings"
	"unicode"
)

// Key 按架构约定拼接 Redis Key 段：用 ":" 连接，自动跳过空段。
//
// 推荐形态：{env}:{channel}:{merchant_id?}:{domain}:{entity}:{id}
// 调用方须自行保证租户/用户维度在段中出现，避免跨租户缓存串用。
func Key(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ":")
}

// JoinPrefix 将前缀与业务 key 组合；prefix 可空。
func JoinPrefix(prefix, key string) string {
	prefix = strings.Trim(prefix, ":")
	key = strings.TrimLeft(key, ":")
	if prefix == "" {
		return key
	}
	if key == "" {
		return prefix
	}
	return prefix + ":" + key
}

// ValidKeySegment 粗检单段是否含空白或控制字符（防注入怪异 key）。
func ValidKeySegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

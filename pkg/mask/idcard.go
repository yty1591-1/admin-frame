package mask

// IDCard 脱敏中国大陆居民身份证（15/18 位）：保留前 4、后 4。
// 非标准长度时对提取出的数字串 Keep(4,4)；过短则 All。
func IDCard(s string) string {
	raw := normalizeIDCard(s)
	if len(raw) == 15 || len(raw) == 18 {
		return Keep(raw, 4, 4)
	}
	if len(raw) >= 8 {
		return Keep(raw, 4, 4)
	}
	if raw != "" {
		return All(raw)
	}
	return All(s)
}

func normalizeIDCard(s string) string {
	var b []rune
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b = append(b, r)
		case r == 'x' || r == 'X':
			b = append(b, 'X')
		}
	}
	return string(b)
}

package mask

import "strings"

// MaskChar 默认掩码字符。
const MaskChar = '*'

// Keep 保留首 head、尾 tail 个 rune，中间以 MaskChar 填充。
//
// head/tail < 0 按 0 处理；若 head+tail >= 总长度，则全部替换为等长掩码（避免泄露）。
func Keep(s string, head, tail int) string {
	if s == "" {
		return ""
	}
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}
	if head+tail >= n {
		return strings.Repeat(string(MaskChar), n)
	}
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < head; i++ {
		b.WriteRune(runes[i])
	}
	for i := 0; i < n-head-tail; i++ {
		b.WriteRune(MaskChar)
	}
	for i := n - tail; i < n; i++ {
		b.WriteRune(runes[i])
	}
	return b.String()
}

// All 将整串替换为等长掩码。
func All(s string) string {
	if s == "" {
		return ""
	}
	return strings.Repeat(string(MaskChar), len([]rune(s)))
}

package persistence

import "testing"

// TestEscapeLikePattern 验证 LIKE 通配符被转义，避免关键词扩大结果集。
func TestEscapeLikePattern(t *testing.T) {
	cases := map[string]string{
		"alice":      "alice",
		"100%":       `100\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
	}
	for input, want := range cases {
		if got := escapeLikePattern(input); got != want {
			t.Fatalf("escapeLikePattern(%q) = %q, want %q", input, got, want)
		}
	}
}

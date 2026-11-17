package domain

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestSecretRuleMatchPlaintext(t *testing.T) {
	t.Parallel()
	rule := SecretRule{MinLen: 32, MaxLen: 32, Encoding: "hex", AutoGenerate: true}
	plain, err := rule.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := rule.MatchPlaintext(plain); err != nil {
		t.Fatalf("generated must match: %v", err)
	}
	if err := rule.MatchPlaintext("zz"); err == nil {
		t.Fatal("invalid hex must fail")
	}
	short := hex.EncodeToString(make([]byte, 8))
	if err := rule.MatchPlaintext(short); err == nil || !strings.Contains(err.Error(), "below min") {
		t.Fatalf("short key: %v", err)
	}
}

func TestSecretRuleRawPreservesWhitespaceBytes(t *testing.T) {
	t.Parallel()
	rule := SecretRule{MinLen: 32, Encoding: "raw", AutoGenerate: true}
	// 首尾各放一个 TrimSpace 会吃掉的字节，回归「生成 32 → 校验成 31」。
	plain := " \n" + strings.Repeat("a", 28) + "\t "
	if len(plain) != 32 {
		t.Fatalf("fixture len=%d", len(plain))
	}
	if err := rule.MatchPlaintext(plain); err != nil {
		t.Fatalf("raw must not trim whitespace bytes: %v", err)
	}
	for i := 0; i < 64; i++ {
		got, err := rule.Generate()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if err := rule.MatchPlaintext(got); err != nil {
			t.Fatalf("generated raw must match (iter %d): %v", i, err)
		}
	}
}

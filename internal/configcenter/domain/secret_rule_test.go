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

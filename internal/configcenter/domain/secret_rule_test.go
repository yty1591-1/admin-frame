package domain

import (
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
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

func TestGenerateForBootstrapAlgorithms(t *testing.T) {
	t.Parallel()
	rsaRule := SecretRule{MinLen: 32, Encoding: "raw", Algorithm: SecretAlgorithmRSAPEM}
	plain, err := rsaRule.GenerateForBootstrap()
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	if err := rsaRule.MatchPlaintext(plain); err != nil {
		t.Fatalf("rsa match: %v", err)
	}
	block, _ := pem.Decode([]byte(plain))
	if block == nil {
		t.Fatal("rsa must be PEM")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		t.Fatalf("parse rsa: %v", err)
	}

	ecRule := SecretRule{MinLen: 32, Encoding: "raw", Algorithm: SecretAlgorithmECP256PEM}
	ecPlain, err := ecRule.GenerateForBootstrap()
	if err != nil {
		t.Fatalf("ec: %v", err)
	}
	if err := ecRule.MatchPlaintext(ecPlain); err != nil {
		t.Fatalf("ec match: %v", err)
	}

	fcmRule := SecretRule{MinLen: 32, Encoding: "raw", Algorithm: SecretAlgorithmFCMSAJSON}
	fcmPlain, err := fcmRule.GenerateForBootstrap()
	if err != nil {
		t.Fatalf("fcm: %v", err)
	}
	if err := fcmRule.MatchPlaintext(fcmPlain); err != nil {
		t.Fatalf("fcm match: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(fcmPlain), &payload); err != nil {
		t.Fatalf("fcm json: %v", err)
	}
	if payload["type"] != "service_account" {
		t.Fatalf("fcm type=%v", payload["type"])
	}

	blocked := SecretRule{MinLen: 8, Encoding: "raw", AutoGenerate: false}
	if _, err := blocked.Generate(); err == nil {
		t.Fatal("Generate must still require AutoGenerate")
	}
	if _, err := blocked.GenerateForBootstrap(); err != nil {
		t.Fatalf("GenerateForBootstrap must ignore AutoGenerate: %v", err)
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

// TestGenerateRawNeverAllWhitespace 守护 raw 播种值不会是纯空白。
//
// MinLen=1 的 raw 规则（第三方 AK/SK 占位）有约 1/13 概率生成空白字节，
// 会被 CreateSecret 的 TrimSpace 判成空明文，使 EnsureAutoSecrets 偶发
// 报 "plaintext is required"——表现为随机失败的 CI 抖动。
func TestGenerateRawNeverAllWhitespace(t *testing.T) {
	t.Parallel()
	rule := SecretRule{MinLen: 1, Encoding: "raw", AutoGenerate: false}
	for i := 0; i < 2000; i++ {
		got, err := rule.GenerateForBootstrap()
		if err != nil {
			t.Fatalf("generate for bootstrap: %v", err)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("raw bootstrap value must not be all whitespace, got %q at iter %d", got, i)
		}
	}
}

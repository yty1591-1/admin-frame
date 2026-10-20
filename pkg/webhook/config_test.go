package webhook_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func distinctKeys(n int) [][]byte {
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = testKey(byte(i + 1))
	}
	return keys
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*webhook.Config)
	}{
		{name: "default valid"},
		{name: "no keys", mutate: func(c *webhook.Config) { c.Keys = nil }},
		{name: "key too short", mutate: func(c *webhook.Config) { c.Keys = [][]byte{make([]byte, webhook.MinKeyBytes-1)} }},
		{name: "key too long", mutate: func(c *webhook.Config) { c.Keys = [][]byte{make([]byte, 4097)} }},
		{name: "too many keys", mutate: func(c *webhook.Config) { c.Keys = distinctKeys(9) }},
		{name: "duplicate keys", mutate: func(c *webhook.Config) {
			c.Keys = [][]byte{testKey(0x2a), testKey(0x2a)}
		}},
		{name: "version with space", mutate: func(c *webhook.Config) { c.Version = "v 1" }},
		{name: "version with comma", mutate: func(c *webhook.Config) { c.Version = "v1,x" }},
		{name: "version too long", mutate: func(c *webhook.Config) { c.Version = strings.Repeat("v", 17) }},
		{name: "header with space", mutate: func(c *webhook.Config) { c.Headers.ID = "bad header" }},
		{name: "header with colon", mutate: func(c *webhook.Config) { c.Headers.Signature = "bad:header" }},
		{name: "header collision", mutate: func(c *webhook.Config) {
			c.Headers.ID = "x-sign"
			c.Headers.Timestamp = "X-Sign"
		}},
		{name: "algorithm without name", mutate: func(c *webhook.Config) { c.Algorithm = namelessAlgorithm{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testSignerConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			err := cfg.Validate()
			if tt.mutate == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, webhook.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestVerifierConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*webhook.Config)
		wantErr bool
	}{
		{name: "default valid"},
		{name: "tolerance too large", mutate: func(c *webhook.Config) { c.Tolerance = 25 * time.Hour }, wantErr: true},
		{name: "body limit too large", mutate: func(c *webhook.Config) { c.MaxBodyBytes = 8<<20 + 1 }, wantErr: true},
		{name: "missing nonce store", mutate: func(c *webhook.Config) { c.Nonce = nil }, wantErr: true},
		{name: "zero tolerance uses default", mutate: func(c *webhook.Config) { c.Tolerance = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testVerifierConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			_, err := webhook.NewVerifier(cfg)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, webhook.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigNormalized(t *testing.T) {
	cfg := webhook.Config{}.Normalized()
	if cfg.Version != webhook.DefaultVersion {
		t.Fatalf("version=%q", cfg.Version)
	}
	if cfg.Headers != webhook.DefaultHeaderNames() {
		t.Fatalf("headers=%+v", cfg.Headers)
	}
	if cfg.Algorithm == nil || cfg.Algorithm.Name() != "hmac-sha256" {
		t.Fatalf("algorithm=%v", cfg.Algorithm)
	}
	if cfg.Tolerance != webhook.DefaultTolerance {
		t.Fatalf("tolerance=%s", cfg.Tolerance)
	}
	if cfg.MaxBodyBytes != webhook.DefaultMaxBodyBytes {
		t.Fatalf("max body=%d", cfg.MaxBodyBytes)
	}
	if cfg.Clock == nil {
		t.Fatal("clock must not be nil")
	}
}

func TestCustomHeaderNames(t *testing.T) {
	headers := webhook.HeaderNames{ID: "x-msg-id", Timestamp: "x-msg-ts", Signature: "x-msg-sig"}
	signerCfg := testSignerConfig()
	signerCfg.Headers = headers
	verifierCfg := testVerifierConfig()
	verifierCfg.Headers = headers

	body := []byte(`{"event":"paid"}`)
	header := signAt(t, mustSigner(t, signerCfg), "msg_custom", body, signingTime)
	for _, name := range []string{headers.ID, headers.Timestamp, headers.Signature} {
		if header.Get(name) == "" {
			t.Fatalf("missing custom header %q", name)
		}
	}
	if _, err := mustVerifier(t, verifierCfg).Verify(t.Context(), header, body); err != nil {
		t.Fatalf("verify custom headers: %v", err)
	}
}

func TestVerifierCopiesKeys(t *testing.T) {
	cfg := testVerifierConfig()
	signer := mustSigner(t, testSignerConfig())
	verifier := mustVerifier(t, cfg)
	header := signAt(t, signer, "msg_keys", []byte(`{"a":1}`), signingTime)

	cfg.Keys[0][0] ^= 0xff

	if _, err := verifier.Verify(t.Context(), header, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("verify after caller mutated keys: %v", err)
	}
}

// namelessAlgorithm 用于验证 Algorithm.Name 为空的配置会被拒绝。
type namelessAlgorithm struct{}

func (namelessAlgorithm) Name() string { return " " }

func (namelessAlgorithm) Sign(key, _ []byte) ([]byte, error) { return key, nil }

func (namelessAlgorithm) Verify(_, _, _ []byte) error { return nil }

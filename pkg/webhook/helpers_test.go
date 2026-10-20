package webhook_test

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/webhook"
)

// signingTime 是所有签名测试使用的固定时间，保证断言确定。
var signingTime = time.Unix(1_700_000_000, 0).UTC()

func testKey(seed byte) []byte {
	return bytes.Repeat([]byte{seed}, 32)
}

func fixedClock(at time.Time) clock.Clock {
	return clock.Func(func() time.Time { return at })
}

func testSignerConfig() webhook.Config {
	return webhook.Config{
		Keys:  [][]byte{testKey(0x2a)},
		Clock: fixedClock(signingTime),
	}
}

func testVerifierConfig() webhook.Config {
	return webhook.Config{
		Keys:  [][]byte{testKey(0x2a)},
		Clock: fixedClock(signingTime),
		Nonce: webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{}),
	}
}

func mustSigner(t *testing.T, cfg webhook.Config) *webhook.Signer {
	t.Helper()
	signer, err := webhook.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

func mustVerifier(t *testing.T, cfg webhook.Config) *webhook.Verifier {
	t.Helper()
	verifier, err := webhook.NewVerifier(cfg)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return verifier
}

func signAt(t *testing.T, signer *webhook.Signer, id string, body []byte, at time.Time) http.Header {
	t.Helper()
	header, err := signer.SignAt(id, body, at)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return header
}

// mutableClock 支持在测试中推进时间，用于 TTL 与并发用例。
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock(now time.Time) *mutableClock {
	return &mutableClock{now: now}
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// alwaysFirstNonce 始终放行，用于基准测试隔离存储开销。
type alwaysFirstNonce struct{}

func (alwaysFirstNonce) Claim(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

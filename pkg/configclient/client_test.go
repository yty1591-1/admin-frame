package configclient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSource 是可切换返回值的快照来源。
type fakeSource struct {
	mu       sync.Mutex
	snapshot Snapshot
	err      error
	calls    int
}

func (f *fakeSource) Load(_ context.Context, _, _ string) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Snapshot{}, f.err
	}
	return f.snapshot, nil
}

func (f *fakeSource) set(snapshot Snapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = snapshot
	f.err = err
}

// newTestClient 用给定验签器构造客户端，时钟可推进。
func newTestClient(t *testing.T, source Source, verifier *Verifier) (*Client, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	client, err := NewClient(ClientConfig{
		Source: source, Verifier: verifier, Env: "dev", Namespace: "platform",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client, &now
}

func TestClientRefreshAndTypedGetters(t *testing.T) {
	snapshot, verifier := buildSignedSnapshot(t, map[string]string{
		"http.timeout": "10s",
		"http.enabled": "true",
		"http.retries": "5",
	})
	source := &fakeSource{}
	client, now := newTestClient(t, source, verifier)
	source.set(snapshot, nil)

	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := client.String("http.timeout", "1s"); got != "10s" {
		t.Fatalf("String = %q, want 10s", got)
	}
	if !client.Bool("http.enabled", false) {
		t.Fatal("Bool should read true")
	}
	if got := client.Int("http.retries", 1); got != 5 {
		t.Fatalf("Int = %d, want 5", got)
	}
	if got := client.Duration("http.timeout", time.Second); got != 10*time.Second {
		t.Fatalf("Duration = %v, want 10s", got)
	}
	if got := client.String("missing", "fallback"); got != "fallback" {
		t.Fatalf("missing String = %q, want fallback", got)
	}
	if got := client.Age(); got != 0 {
		t.Fatalf("Age = %v, want 0", got)
	}
	*now = now.Add(30 * time.Second)
	if got := client.Age(); got != 30*time.Second {
		t.Fatalf("Age = %v, want 30s", got)
	}
}

func TestClientRefreshKeepsLastGoodSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	source := &fakeSource{}
	client, _ := newTestClient(t, source, verifier)
	source.set(snapshot, nil)
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	source.set(Snapshot{}, errors.New("mysql unavailable"))
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("refresh must report source failure")
	}
	if client.LastError() == nil {
		t.Fatal("LastError must record failure")
	}
	if !client.Degraded() {
		t.Fatal("client must be degraded after failed refresh")
	}
	if value, ok := client.Get("http.timeout"); !ok || value != "10s" {
		t.Fatalf("cached value lost after failed refresh: %q %v", value, ok)
	}
	if _, ok := client.Snapshot(); !ok {
		t.Fatal("snapshot must remain available after failed refresh")
	}
}

func TestClientRejectsInvalidSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.Values["log.level"] = "debug"
	source := &fakeSource{}
	client, _ := newTestClient(t, source, verifier)
	source.set(snapshot, nil)

	err := client.Refresh(context.Background())
	if !errors.Is(err, ErrSnapshotValueMismatch) {
		t.Fatalf("refresh invalid snapshot = %v, want ErrSnapshotValueMismatch", err)
	}
	if len(client.Values()) != 0 {
		t.Fatal("invalid snapshot must not be loaded")
	}
}

func TestClientRequiresConfiguration(t *testing.T) {
	_, verifier := signedSnapshot(t)
	base := ClientConfig{Source: &fakeSource{}, Verifier: verifier, Env: "dev", Namespace: "platform"}

	noSource := base
	noSource.Source = nil
	if _, err := NewClient(noSource); err == nil {
		t.Fatal("client without source must be rejected")
	}
	noVerifier := base
	noVerifier.Verifier = nil
	if _, err := NewClient(noVerifier); err == nil {
		t.Fatal("client without verifier must be rejected")
	}
	noEnv := base
	noEnv.Env = ""
	if _, err := NewClient(noEnv); err == nil {
		t.Fatal("client without env must be rejected")
	}
}

package configclient

import (
	"context"
	"crypto/ed25519"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientWatchReceivesEvents 验证 Watch 订阅刷新事件并在取消后关闭。
func TestClientWatchReceivesEvents(t *testing.T) {
	publicKey, privateKey := testKey(t)
	signer, err := NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	verifier, err := NewVerifier(map[string]ed25519.PublicKey{"k1": publicKey})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	source := &namespaceSource{
		snapshots: map[string]Snapshot{
			"platform": buildSignedFor(t, signer, "platform", map[string]string{"addr": ":9000"}),
		},
	}
	client, err := NewClient(ClientConfig{Source: source, Verifier: verifier, Env: "dev", Namespace: "platform"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events := client.Watch(ctx)
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	select {
	case event := <-events:
		if event.Namespace != "platform" || !event.Loaded {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not receive refresh event")
	}

	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("watch channel not closed after context cancel")
		}
	}
}

// TestClientWatchDropsSlowConsumer 验证慢消费者丢事件并触发回调。
func TestClientWatchDropsSlowConsumer(t *testing.T) {
	publicKey, privateKey := testKey(t)
	signer, err := NewSigner("k1", privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	verifier, err := NewVerifier(map[string]ed25519.PublicKey{"k1": publicKey})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	source := &namespaceSource{
		snapshots: map[string]Snapshot{
			"platform": buildSignedFor(t, signer, "platform", map[string]string{"addr": ":9000"}),
		},
	}
	var dropped atomic.Int64
	client, err := NewClient(ClientConfig{
		Source: source, Verifier: verifier, Env: "dev", Namespace: "platform",
		OnWatchDropped: func() { dropped.Add(1) },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = client.Watch(ctx) // 不消费，触发积压

	for i := 0; i < 20; i++ {
		if err := client.Refresh(context.Background()); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}
	if dropped.Load() == 0 {
		t.Fatal("slow consumer must drop events")
	}
}

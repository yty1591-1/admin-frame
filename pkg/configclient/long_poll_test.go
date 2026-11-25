package configclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLongPollerPreservesTransportAndExtendsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Millisecond):
			w.WriteHeader(http.StatusNotModified)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	original := &http.Client{Timeout: time.Millisecond, Transport: http.DefaultTransport}
	poller, err := NewLongPoller(LongPollerConfig{BaseURL: server.URL, ServiceIdentity: "test", HTTPClient: original, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.Wait(context.Background(), "dev", map[string]int64{"platform": 1}); err != nil {
		t.Fatalf("idle poll failed: %v", err)
	}
	if original.Timeout != time.Millisecond || poller.http.Transport != original.Transport {
		t.Fatal("shared client or transport changed")
	}
}

type scriptedNotifier struct {
	calls  int
	source *namespaceSource
	next   Snapshot
}

func (n *scriptedNotifier) Wait(ctx context.Context, _ string, _ map[string]int64) ([]string, error) {
	n.calls++
	switch n.calls {
	case 1:
		return nil, context.DeadlineExceeded
	case 2:
		n.source.set("platform", n.next, nil)
		return []string{"platform"}, nil
	default:
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

func TestLongPollContinuesAfterRequestTimeoutAndRefreshesOnlyChangedNamespace(t *testing.T) {
	source := &namespaceSource{snapshots: map[string]Snapshot{
		"platform": buildFor(t, "platform", map[string]string{"value": "old"}),
		"identity": buildFor(t, "identity", map[string]string{"enabled": "true"}),
	}, errs: map[string]error{}}
	notifier := &scriptedNotifier{source: source, next: buildFor(t, "platform", map[string]string{"value": "new"})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := NewClient(ClientConfig{Source: source, Env: "dev", Namespaces: []string{"platform", "identity"}, Notifier: notifier,
		OnRefresh: func(e RefreshEvent) {
			if e.Snapshot.Values["value"] == "new" {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx, time.Second) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not recover from request timeout")
	}
	if got := client.String("value", ""); got != "new" {
		t.Fatalf("value=%q; refresh stopped after timeout", got)
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.calls["platform"] != 3 || source.calls["identity"] != 2 {
		t.Fatalf("unexpected loads: %v", source.calls)
	}
}

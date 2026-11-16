package configclient

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// namespaceSource 按 namespace 返回不同快照，并支持注入单 namespace 失败。
type namespaceSource struct {
	mu        sync.Mutex
	snapshots map[string]Snapshot
	errs      map[string]error
	calls     map[string]int
}

func (s *namespaceSource) Load(_ context.Context, _, namespace string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[namespace]++
	if err := s.errs[namespace]; err != nil {
		return Snapshot{}, err
	}
	return s.snapshots[namespace], nil
}

func (s *namespaceSource) set(namespace string, snapshot Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[namespace] = snapshot
	s.errs[namespace] = err
}

func buildFor(t *testing.T, namespace string, values map[string]string) Snapshot {
	t.Helper()
	snap := buildSnapshot(t, values)
	snap.Namespace = namespace
	return snap
}

func TestClientMultiNamespaceMergesValues(t *testing.T) {
	source := &namespaceSource{
		snapshots: map[string]Snapshot{
			"platform": buildFor(t, "platform", map[string]string{"server.addr": ":9000"}),
			"identity": buildFor(t, "identity", map[string]string{"identity.enabled": "true"}),
		},
	}
	var events []RefreshEvent
	client, err := NewClient(ClientConfig{
		Source: source, Env: "dev",
		Namespaces: []string{"identity", "platform"}, PrimaryNamespace: "platform",
		OnRefresh: func(event RefreshEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := client.String("server.addr", ""); got != ":9000" {
		t.Fatalf("server.addr = %q", got)
	}
	if !client.Bool("identity.enabled", false) {
		t.Fatal("identity.enabled should read true")
	}
	if snapshot, ok := client.SnapshotFor("identity"); !ok || snapshot.Namespace != "identity" {
		t.Fatalf("identity snapshot = %+v, %v", snapshot, ok)
	}
	if snapshot, ok := client.Snapshot(); !ok || snapshot.Namespace != "platform" {
		t.Fatalf("primary snapshot = %+v, %v; want platform", snapshot, ok)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.Err != nil || !event.Loaded {
			t.Fatalf("unexpected event: %+v", event)
		}
	}
}

func TestClientMultiNamespacePartialFailureKeepsOld(t *testing.T) {
	source := &namespaceSource{
		snapshots: map[string]Snapshot{
			"platform": buildFor(t, "platform", map[string]string{"server.addr": ":9000"}),
			"identity": buildFor(t, "identity", map[string]string{"identity.enabled": "true"}),
		},
		errs: map[string]error{},
	}
	client, err := NewClient(ClientConfig{
		Source: source, Env: "dev",
		Namespaces: []string{"platform", "identity"},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	source.set("platform",
		buildFor(t, "platform", map[string]string{"server.addr": ":9100"}),
		nil)
	source.set("identity", Snapshot{}, errors.New("db unavailable"))

	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("partial failure must surface an error")
	}
	if got := client.String("server.addr", ""); got != ":9100" {
		t.Fatalf("platform should update, got %q", got)
	}
	if !client.Bool("identity.enabled", false) {
		t.Fatal("identity should keep last good snapshot")
	}
	if !client.Degraded() {
		t.Fatal("client must be degraded when any namespace fails")
	}
}

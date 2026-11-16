package configcenterruntime

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

type fakeRefreshSource struct {
	snapshot configclient.Snapshot
	err      error
}

func (f *fakeRefreshSource) Load(context.Context, string, string) (configclient.Snapshot, error) {
	if f.err != nil {
		return configclient.Snapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestNewRefreshClientWithoutSource(t *testing.T) {
	client, err := NewRefreshClient(RefreshConfig{Env: "dev", Namespace: "platform"})
	if err != nil || client != nil {
		t.Fatalf("client = %v, %v; want nil, nil", client, err)
	}
}

func TestRefreshClientLoadsSnapshot(t *testing.T) {
	snapshot := configclient.Snapshot{
		Env: "dev", Namespace: "platform", ReleaseID: 1, ReleaseNo: 1,
		Values: map[string]string{"server.addr": ":9000"},
	}
	client, err := NewRefreshClient(RefreshConfig{
		Source: &fakeRefreshSource{snapshot: snapshot},
		Env:    "dev", Namespace: "platform",
	})
	if err != nil {
		t.Fatalf("new refresh client: %v", err)
	}
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := client.String("server.addr", ""); got != ":9000" {
		t.Fatalf("value = %q, want :9000", got)
	}
}

func TestRunRefreshStopsOnContextCancel(t *testing.T) {
	snapshot := configclient.Snapshot{
		Env: "dev", Namespace: "platform", ReleaseID: 1,
		Values: map[string]string{"server.addr": ":9000"},
	}
	client, err := NewRefreshClient(RefreshConfig{
		Source: &fakeRefreshSource{snapshot: snapshot},
		Env:    "dev", Namespace: "platform",
	})
	if err != nil {
		t.Fatalf("new refresh client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runRefresh(ctx, client, 10*time.Millisecond, nil) }()

	deadline := time.Now().Add(2 * time.Second)
	for client.String("server.addr", "") == "" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for refresh")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runRefresh: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runRefresh did not stop")
	}
}

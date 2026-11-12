package configcenterruntime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// fakeRefreshSource 是可切换返回值的快照来源。
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

// loadSeedSnapshot 生成签名快照文件并返回快照与公钥配置串。
func loadSeedSnapshot(t *testing.T, values map[string]string) (snapshot configclient.Snapshot, publicKeys string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.json")
	publicKeys = writeSignedSnapshot(t, path, values)
	store, err := configclient.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	snapshot, err = store.Load(context.Background())
	if err != nil {
		t.Fatalf("load seed snapshot: %v", err)
	}
	return snapshot, publicKeys
}

func TestNewRefreshClientDisabledWithoutPublicKeys(t *testing.T) {
	client, err := NewRefreshClient(RefreshConfig{
		Source: &fakeRefreshSource{}, Env: "dev", Namespace: "platform",
	})
	if err != nil || client != nil {
		t.Fatalf("client = %v, %v; want nil, nil", client, err)
	}
}

func TestNewRefreshClientRejectsInvalidPublicKeys(t *testing.T) {
	if _, err := NewRefreshClient(RefreshConfig{
		Source: &fakeRefreshSource{}, PublicKeys: "k1=not-base64", Env: "dev", Namespace: "platform",
	}); err == nil {
		t.Fatal("invalid public keys must be rejected")
	}
}

func TestRefreshClientLoadsAndPersistsSnapshot(t *testing.T) {
	snapshot, publicKeys := loadSeedSnapshot(t, map[string]string{"server.addr": ":9000"})
	livePath := filepath.Join(t.TempDir(), "live.json")
	client, err := NewRefreshClient(RefreshConfig{
		Source:       &fakeRefreshSource{snapshot: snapshot},
		PublicKeys:   publicKeys,
		SnapshotFile: livePath,
		Env:          "dev",
		Namespace:    "platform",
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

	keys, err := configclient.ParsePublicKeys(publicKeys)
	if err != nil {
		t.Fatalf("parse public keys: %v", err)
	}
	verifier, err := configclient.NewVerifier(keys)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	store, err := configclient.NewFileStore(livePath)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	persisted, err := configclient.LoadFromStore(context.Background(), store, verifier)
	if err != nil {
		t.Fatalf("load persisted snapshot: %v", err)
	}
	if persisted.SnapshotHash != snapshot.SnapshotHash {
		t.Fatalf("persisted hash = %q, want %q", persisted.SnapshotHash, snapshot.SnapshotHash)
	}
}

func TestRunRefreshStopsOnContextCancel(t *testing.T) {
	snapshot, publicKeys := loadSeedSnapshot(t, map[string]string{"server.addr": ":9000"})
	client, err := NewRefreshClient(RefreshConfig{
		Source: &fakeRefreshSource{snapshot: snapshot}, PublicKeys: publicKeys,
		Env: "dev", Namespace: "platform",
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
			t.Fatal("refresh did not load snapshot in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run refresh error = %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run refresh did not stop after cancel")
	}
}

func TestRefreshLoggerDeduplicatesTransitions(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	log := refreshLogger(logger)
	snapshot := configclient.Snapshot{Env: "dev", Namespace: "platform", ReleaseID: 1}

	log(configclient.RefreshEvent{Snapshot: snapshot, Loaded: true})
	log(configclient.RefreshEvent{Snapshot: snapshot, Loaded: true})
	snapshot.ReleaseID = 2
	log(configclient.RefreshEvent{Snapshot: snapshot, Loaded: true})
	log(configclient.RefreshEvent{Snapshot: snapshot, Loaded: true, Degraded: true, Err: errors.New("mysql unavailable")})
	log(configclient.RefreshEvent{Snapshot: snapshot, Loaded: true, Degraded: true, Err: errors.New("mysql unavailable")})

	output := buffer.String()
	if got := strings.Count(output, "snapshot loaded"); got != 2 {
		t.Fatalf("loaded logs = %d, want 2; output=%s", got, output)
	}
	if got := strings.Count(output, "refresh failed"); got != 1 {
		t.Fatalf("failure logs = %d, want 1; output=%s", got, output)
	}
}

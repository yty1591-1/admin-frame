package configclient

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	snapshot, _ := signedSnapshot(t)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if loaded.SnapshotHash != snapshot.SnapshotHash || loaded.Signature != snapshot.Signature {
		t.Fatalf("loaded snapshot mismatch: %+v", loaded)
	}
	if loaded.Values["http.timeout"] != "10s" {
		t.Fatalf("loaded value = %q, want 10s", loaded.Values["http.timeout"])
	}
}

func TestLoadFromStoreVerifiesSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	loaded, err := LoadFromStore(context.Background(), store, verifier)
	if err != nil {
		t.Fatalf("load from store: %v", err)
	}
	if loaded.Values["http.timeout"] != "10s" {
		t.Fatalf("loaded value = %q, want 10s", loaded.Values["http.timeout"])
	}

	tampered := snapshot
	tampered.Values = map[string]string{"http.timeout": "99s", "log.level": "info"}
	if err := store.Save(context.Background(), tampered); err != nil {
		t.Fatalf("save tampered snapshot: %v", err)
	}
	if _, err := LoadFromStore(context.Background(), store, verifier); err == nil {
		t.Fatal("tampered snapshot must be rejected")
	}
}

func TestFileStoreRejectsEmptyPath(t *testing.T) {
	if _, err := NewFileStore(""); err == nil {
		t.Fatal("empty snapshot path must be rejected")
	}
}

func TestClientFallsBackToLocalSnapshot(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	source := &fakeSource{}
	source.set(Snapshot{}, errors.New("mysql unavailable"))
	client, err := NewClient(ClientConfig{
		Source: source, Verifier: verifier, Env: "dev", Namespace: "platform", Store: store,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Refresh(context.Background()); err == nil {
		t.Fatal("refresh must report source failure")
	}
	if got := client.String("http.timeout", "1s"); got != "10s" {
		t.Fatalf("fallback value = %q, want 10s", got)
	}
	if !client.Degraded() {
		t.Fatal("client must be degraded while serving local snapshot")
	}

	source.set(snapshot, nil)
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("recover refresh: %v", err)
	}
	if client.Degraded() {
		t.Fatal("client must leave degraded after successful refresh")
	}
}

func TestClientRejectsLocalSnapshotForOtherNamespace(t *testing.T) {
	snapshot, verifier := signedSnapshot(t)
	snapshot.Namespace = "iam"
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	source := &fakeSource{}
	source.set(Snapshot{}, errors.New("mysql unavailable"))
	client, err := NewClient(ClientConfig{
		Source: source, Verifier: verifier, Env: "dev", Namespace: "platform", Store: store,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_ = client.Refresh(context.Background())
	if len(client.Values()) != 0 {
		t.Fatal("snapshot from another namespace must not be loaded")
	}
}

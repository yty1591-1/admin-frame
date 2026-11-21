package configcenterruntime

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

type countingHandler struct {
	info int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelInfo && r.Message == "configcenter snapshot loaded" {
		h.info++
	}
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

func TestRefreshLoggerDedupsPerNamespace(t *testing.T) {
	handler := &countingHandler{}
	logFn := refreshLogger(slog.New(handler))
	if logFn == nil {
		t.Fatal("refreshLogger returned nil")
	}

	emit := func(ns string, release int64) {
		logFn(configclient.RefreshEvent{
			Loaded: true,
			Snapshot: configclient.Snapshot{
				Env: "prod", Namespace: ns, ReleaseID: release, ReleaseNo: 1,
				Values: map[string]string{"k": "v"},
			},
		})
	}

	// 一轮多 namespace：各打一次。
	emit("audit", 1)
	emit("iam", 2)
	emit("platform", 3)
	if handler.info != 3 {
		t.Fatalf("first round info=%d, want 3", handler.info)
	}

	// 同 release 再轮询：不应再打 INFO。
	emit("audit", 1)
	emit("iam", 2)
	emit("platform", 3)
	if handler.info != 3 {
		t.Fatalf("unchanged round info=%d, want 3", handler.info)
	}

	// 仅 platform 升版：只多 1 条。
	emit("platform", 4)
	emit("audit", 1)
	if handler.info != 4 {
		t.Fatalf("after platform bump info=%d, want 4", handler.info)
	}
}

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

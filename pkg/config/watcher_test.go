package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
)

func newWatcher(t *testing.T, opts config.WatchOptions[testConfig]) *config.Watcher[testConfig] {
	t.Helper()
	watcher, err := config.NewWatcher(opts)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	return watcher
}

func TestNewWatcherValidatesOptions(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	envOnly := newLoader(t, config.Options[testConfig]{EnvPrefix: "APP", LookupEnv: envLookup(nil)})
	fileLoader := newLoader(t, testOptions(path))

	tests := []struct {
		name string
		opts config.WatchOptions[testConfig]
	}{
		{name: "nil loader", opts: config.WatchOptions[testConfig]{}},
		{name: "no files", opts: config.WatchOptions[testConfig]{Loader: envOnly}},
		{name: "interval too small", opts: config.WatchOptions[testConfig]{Loader: fileLoader, Interval: time.Millisecond}},
		{name: "interval too large", opts: config.WatchOptions[testConfig]{Loader: fileLoader, Interval: time.Hour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.NewWatcher(tt.opts); !errors.Is(err, config.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewWatcherInitialLoadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := config.NewWatcher(config.WatchOptions[testConfig]{
		Loader: newLoader(t, testOptions(path)),
	})
	if !errors.Is(err, config.ErrMissingFile) {
		t.Fatalf("err=%v want ErrMissingFile", err)
	}
}

func TestWatcherCurrent(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	if got := watcher.Current(); got == nil || got.Addr != ":8081" {
		t.Fatalf("current=%+v", got)
	}

	var nilWatcher *config.Watcher[testConfig]
	if nilWatcher.Current() != nil {
		t.Fatal("nil watcher Current must return nil")
	}
}

func TestWatcherManualReload(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	events := make(chan config.Event[testConfig], 4)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })

	writeFile(t, dir, "app.yaml", "addr: \":8082\"\n")
	if err := watcher.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	event := waitEvent(t, events, time.Second, func(config.Event[testConfig]) bool { return true })
	if !event.Changed() {
		t.Fatalf("event should report change: %+v", event)
	}
	if event.New.Addr != ":8082" {
		t.Fatalf("new addr=%q", event.New.Addr)
	}
	if event.Old.Addr != ":8081" {
		t.Fatalf("old addr=%q", event.Old.Addr)
	}
	if got := watcher.Current(); got.Addr != ":8082" {
		t.Fatalf("current addr=%q", got.Addr)
	}
}

func TestWatcherPollingDetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{
		Loader:   newLoader(t, testOptions(path)),
		Interval: 10 * time.Millisecond,
	})
	events := make(chan config.Event[testConfig], 8)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })
	if err := watcher.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer watcher.Close()

	writeFile(t, dir, "app.yaml", "addr: \":8082\"\n")
	event := waitEvent(t, events, 2*time.Second, func(ev config.Event[testConfig]) bool {
		return ev.Err == nil && ev.New != nil && ev.New.Addr == ":8082"
	})
	if !event.Changed() {
		t.Fatalf("event should report change: %+v", event)
	}
	waitFor(t, time.Second, func() bool { return watcher.Current().Addr == ":8082" })
}

func TestWatcherKeepsSnapshotOnInvalidReload(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	events := make(chan config.Event[testConfig], 4)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })

	writeFile(t, dir, "app.yaml", "addr: [broken\n")
	if err := watcher.Reload(); !errors.Is(err, config.ErrMalformedFile) {
		t.Fatalf("err=%v want ErrMalformedFile", err)
	}
	if got := watcher.Current(); got.Addr != ":8081" {
		t.Fatalf("snapshot replaced on failure: %+v", got)
	}
	event := waitEvent(t, events, time.Second, func(ev config.Event[testConfig]) bool { return ev.Err != nil })
	if event.New != nil {
		t.Fatalf("failed event must not carry new snapshot: %+v", event)
	}
	if event.Changed() {
		t.Fatal("failed event must not report change")
	}
}

func TestWatcherDedupesRepeatedErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{
		Loader:   newLoader(t, testOptions(path)),
		Interval: 10 * time.Millisecond,
	})
	events := make(chan config.Event[testConfig], 16)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })
	if err := watcher.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer watcher.Close()

	writeFile(t, dir, "app.yaml", "addr: [broken\n")
	waitEvent(t, events, 2*time.Second, func(ev config.Event[testConfig]) bool { return ev.Err != nil })
	time.Sleep(100 * time.Millisecond)
	if extra := len(events); extra != 0 {
		t.Fatalf("repeated identical errors should be deduped, got %d extra events", extra)
	}
	if got := watcher.Current(); got.Addr != ":8081" {
		t.Fatalf("snapshot changed on failure: %+v", got)
	}
}

func TestWatcherRecoversAfterFix(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{
		Loader:   newLoader(t, testOptions(path)),
		Interval: 10 * time.Millisecond,
	})
	events := make(chan config.Event[testConfig], 16)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })
	if err := watcher.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer watcher.Close()

	writeFile(t, dir, "app.yaml", "addr: [broken\n")
	waitEvent(t, events, 2*time.Second, func(ev config.Event[testConfig]) bool { return ev.Err != nil })

	writeFile(t, dir, "app.yaml", "addr: \":8083\"\n")
	waitEvent(t, events, 2*time.Second, func(ev config.Event[testConfig]) bool {
		return ev.Err == nil && ev.New != nil && ev.New.Addr == ":8083"
	})
}

func TestWatcherMissingFileAfterStart(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{
		Loader:   newLoader(t, testOptions(path)),
		Interval: 10 * time.Millisecond,
	})
	events := make(chan config.Event[testConfig], 8)
	watcher.Subscribe(func(ev config.Event[testConfig]) { events <- ev })
	if err := watcher.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer watcher.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	event := waitEvent(t, events, 5*time.Second, func(ev config.Event[testConfig]) bool { return ev.Err != nil })
	if !errors.Is(event.Err, config.ErrMissingFile) {
		t.Fatalf("err=%v want ErrMissingFile", event.Err)
	}
	if got := watcher.Current(); got.Addr != ":8081" {
		t.Fatalf("snapshot changed: %+v", got)
	}
}

func TestWatcherLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{
		Loader:   newLoader(t, testOptions(path)),
		Interval: 20 * time.Millisecond,
	})
	if err := watcher.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := watcher.Start(); !errors.Is(err, config.ErrWatcherStarted) {
		t.Fatalf("second start err=%v want ErrWatcherStarted", err)
	}
	watcher.Close()
	watcher.Close()

	if err := watcher.Start(); !errors.Is(err, config.ErrWatcherClosed) {
		t.Fatalf("start after close err=%v want ErrWatcherClosed", err)
	}
	if err := watcher.Reload(); !errors.Is(err, config.ErrWatcherClosed) {
		t.Fatalf("reload after close err=%v want ErrWatcherClosed", err)
	}
}

func TestWatcherCloseWithoutStart(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	watcher.Close()
	watcher.Close()
}

func TestWatcherUnsubscribe(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	called := 0
	cancel := watcher.Subscribe(func(config.Event[testConfig]) { called++ })
	cancel()
	cancel()

	writeFile(t, dir, "app.yaml", "addr: \":8082\"\n")
	if err := watcher.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if called != 0 {
		t.Fatalf("unsubscribed callback invoked %d times", called)
	}
}

func TestWatcherSubscribeNil(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	watcher := newWatcher(t, config.WatchOptions[testConfig]{Loader: newLoader(t, testOptions(path))})
	cancel := watcher.Subscribe(nil)
	cancel()

	var nilWatcher *config.Watcher[testConfig]
	nilWatcher.Subscribe(func(config.Event[testConfig]) {})
}

func TestWatcherNilMethods(t *testing.T) {
	var watcher *config.Watcher[testConfig]
	if err := watcher.Start(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("start err=%v", err)
	}
	if err := watcher.Reload(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("reload err=%v", err)
	}
	watcher.Close()
}

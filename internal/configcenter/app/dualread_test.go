package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// fakeSnapshotProvider 返回固定快照或固定错误。
type fakeSnapshotProvider struct {
	snapshot app.Snapshot
	err      error
	calls    int
}

func (f *fakeSnapshotProvider) Snapshot(_ context.Context, _, _ string) (app.Snapshot, error) {
	f.calls++
	if f.err != nil {
		return app.Snapshot{}, f.err
	}
	return f.snapshot, nil
}

// fakeDualReadReporter 记录双读上报。
type fakeDualReadReporter struct {
	results []app.DualReadResult
}

func (f *fakeDualReadReporter) ReportDualRead(_ context.Context, _, _ string, result app.DualReadResult) {
	f.results = append(f.results, result)
}

func TestDualReaderCenterPriority(t *testing.T) {
	provider := &fakeSnapshotProvider{snapshot: app.Snapshot{
		ReleaseID: 7, Version: 3,
		Values: map[string]string{
			"http.timeout": "30s",
			"log.level":    "debug",
			"new.key":      "x",
		},
	}}
	reporter := &fakeDualReadReporter{}
	reader, err := app.NewDualReader(provider, reporter)
	if err != nil {
		t.Fatalf("new dual reader: %v", err)
	}
	baseline := map[string]string{
		"http.timeout": "10s",
		"log.level":    "debug",
		"legacy.key":   "1",
	}

	result, err := reader.Resolve(context.Background(), "dev", "platform", baseline)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.ReleaseID != 7 || result.Version != 3 || result.Degraded {
		t.Fatalf("result metadata mismatch: %+v", result)
	}
	if result.Values["http.timeout"] != "30s" || result.Values["log.level"] != "debug" ||
		result.Values["new.key"] != "x" || result.Values["legacy.key"] != "1" {
		t.Fatalf("center values must win while baseline fills gaps: %+v", result.Values)
	}
	if !result.HasMismatch() || len(result.Mismatches) != 1 {
		t.Fatalf("expected one mismatch: %+v", result.Mismatches)
	}
	diff := result.Mismatches[0]
	if diff.Key != "http.timeout" || diff.BaselineHash != domain.HashValue("10s") ||
		diff.CenterHash != domain.HashValue("30s") {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if len(result.MissingInCenter) != 1 || result.MissingInCenter[0] != "legacy.key" {
		t.Fatalf("missing in center mismatch: %+v", result.MissingInCenter)
	}
	if len(result.MissingInBaseline) != 1 || result.MissingInBaseline[0] != "new.key" {
		t.Fatalf("missing in baseline mismatch: %+v", result.MissingInBaseline)
	}
	if len(reporter.results) != 1 {
		t.Fatalf("reporter must receive one result, got %d", len(reporter.results))
	}
	if baseline["http.timeout"] != "10s" {
		t.Fatal("baseline map must not be mutated")
	}
}

func TestDualReaderConsistent(t *testing.T) {
	values := map[string]string{"http.timeout": "10s", "log.level": "info"}
	provider := &fakeSnapshotProvider{snapshot: app.Snapshot{ReleaseID: 1, Version: 1, Values: values}}
	reporter := &fakeDualReadReporter{}
	reader, err := app.NewDualReader(provider, reporter)
	if err != nil {
		t.Fatalf("new dual reader: %v", err)
	}

	baseline := map[string]string{"http.timeout": "10s", "log.level": "info"}
	result, err := reader.Resolve(context.Background(), "dev", "platform", baseline)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.HasMismatch() || len(result.MissingInCenter) != 0 || len(result.MissingInBaseline) != 0 {
		t.Fatalf("consistent read must have no diff: %+v", result)
	}
	if len(reporter.results) != 1 || reporter.results[0].HasMismatch() {
		t.Fatalf("reporter mismatch: %+v", reporter.results)
	}
}

func TestDualReaderDegradesToBaseline(t *testing.T) {
	provider := &fakeSnapshotProvider{err: errors.New("center down")}
	reporter := &fakeDualReadReporter{}
	reader, err := app.NewDualReader(provider, reporter)
	if err != nil {
		t.Fatalf("new dual reader: %v", err)
	}
	baseline := map[string]string{"http.timeout": "10s"}

	result, resolveErr := reader.Resolve(context.Background(), "dev", "platform", baseline)
	if !errors.Is(resolveErr, apperr.ErrUnavailable) {
		t.Fatalf("center outage must be unavailable, got %v", resolveErr)
	}
	if !result.Degraded || result.Values["http.timeout"] != "10s" {
		t.Fatalf("degraded result mismatch: %+v", result)
	}
	if len(reporter.results) != 1 || !reporter.results[0].Degraded {
		t.Fatalf("reporter must observe degraded read: %+v", reporter.results)
	}
}

func TestDualReaderValidation(t *testing.T) {
	provider := &fakeSnapshotProvider{}
	reader, err := app.NewDualReader(provider, nil)
	if err != nil {
		t.Fatalf("nil reporter must be allowed: %v", err)
	}

	if _, err := reader.Resolve(context.Background(), "dev", "Platform", nil); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("invalid namespace must be bad request, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatal("provider must not be called for invalid scope")
	}

	if _, err := app.NewDualReader(nil, nil); err == nil {
		t.Fatal("nil provider must be rejected")
	}
}

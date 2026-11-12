package app_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

var testNow = time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)

// fakeEmitter 记录审计事件，便于断言动作与资源。
type fakeEmitter struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (f *fakeEmitter) Emit(_ context.Context, event audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

func (f *fakeEmitter) all() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Event(nil), f.events...)
}

// seqIDs 是确定性的自增 ID 生成器。
type seqIDs struct {
	mu   sync.Mutex
	next int64
}

func newIDs() *seqIDs {
	return &seqIDs{next: 900000}
}

func (s *seqIDs) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return s.next, nil
}

func (s *seqIDs) NextString() (string, error) {
	next, err := s.Next()
	return strconv.FormatInt(next, 10), err
}

// testSpecs 是 item 用例的键声明集合。
func testSpecs(t *testing.T) *spec.Registry {
	t.Helper()
	registry := spec.NewRegistry()
	err := registry.Register(
		domain.KeySpec{
			Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeDuration,
			Default: "10s", Description: "HTTP 超时", Owner: "team-platform", Reloadable: true,
		},
		domain.KeySpec{
			Namespace: "platform", Key: "http.retries", ValueType: domain.ValueTypeInt,
			Default: "3", Description: "重试次数", Owner: "team-platform",
			MinInt: int64Ptr(1), MaxInt: int64Ptr(10),
		},
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
			Enum: []string{"debug", "info", "warn"},
		},
		domain.KeySpec{
			Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
			Description: "SMTP 口令", Owner: "team-platform", Sensitive: true,
		},
	)
	if err != nil {
		t.Fatalf("register specs: %v", err)
	}
	return registry
}

func newItemService(t *testing.T) (*app.ItemService, *memory.Repositories, *fakeEmitter) {
	t.Helper()
	repos := memory.New()
	emitter := &fakeEmitter{}
	service, err := app.NewItemService(repos.Items, testSpecs(t), emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new item service: %v", err)
	}
	return service, repos, emitter
}

func newReleaseService(t *testing.T) (*app.ReleaseService, *memory.Repositories, *fakeEmitter) {
	t.Helper()
	repos := memory.New()
	emitter := &fakeEmitter{}
	service, err := app.NewReleaseService(repos.Items, repos.Releases, repos.Pointers, emitter, newIDs(),
		app.Options{Service: "admin-api", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("new release service: %v", err)
	}
	return service, repos, emitter
}

// seedItem 直接写入一个配置键与版本 1，返回键 ID。
func seedItem(t *testing.T, repos *memory.Repositories, id int64, key, value string) int64 {
	t.Helper()
	item := domain.Item{
		ID: id, Env: domain.EnvDev, Namespace: "platform", Key: key,
		ValueType: domain.ValueTypeString, Description: "测试键", Owner: "team-platform",
		Status: domain.ItemStatusDraft, CurrentVersion: 1, Version: 1,
		CreatedBy: "admin-1", UpdatedBy: "admin-1", CreatedAt: testNow, UpdatedAt: testNow,
	}
	if err := repos.Items.Create(context.Background(), item); err != nil {
		t.Fatalf("seed item %s: %v", key, err)
	}
	version := domain.ItemVersion{
		ID: id + 1000, ItemID: item.ID, Version: 1, ValueText: value,
		ValueHash: domain.HashValue(value), ChangeSummary: "seed", CreatedBy: "admin-1", CreatedAt: testNow,
	}
	if err := repos.Items.AppendVersion(context.Background(), version); err != nil {
		t.Fatalf("seed item version %s: %v", key, err)
	}
	return item.ID
}

func int64Ptr(v int64) *int64 { return &v }

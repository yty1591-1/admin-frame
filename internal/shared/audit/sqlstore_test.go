package audit_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// failingEmitter 记录调用并返回固定错误，用于验证 MultiEmitter 失败即停。
type failingEmitter struct {
	calls int
	err   error
}

func (f *failingEmitter) Emit(_ context.Context, _ audit.Event) error {
	f.calls++
	return f.err
}

// TestSQLEmitterRequiresDatabase 验证未配置审计库时失败关闭，禁止静默丢弃。
func TestSQLEmitterRequiresDatabase(t *testing.T) {
	emitter := audit.SQLEmitter{}
	if err := emitter.Emit(context.Background(), validEvent()); err == nil {
		t.Fatal("emitter without database must fail closed")
	}
}

// TestSQLEmitterRejectsInvalidEvent 验证落库前执行审计事件校验。
func TestSQLEmitterRejectsInvalidEvent(t *testing.T) {
	event := validEvent()
	event.Service = ""
	emitter := audit.SQLEmitter{DB: nil}
	if err := emitter.Emit(context.Background(), event); err == nil {
		t.Fatal("invalid event must be rejected before database access")
	}
}

// TestMultiEmitterStopsOnFailure 验证多出口按顺序执行且任一口失败即停止。
func TestMultiEmitterStopsOnFailure(t *testing.T) {
	first := &failingEmitter{}
	second := &failingEmitter{}
	ctx := context.Background()
	if err := (audit.MultiEmitter{first, second}).Emit(ctx, validEvent()); err != nil {
		t.Fatal("nil failures should succeed")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", first.calls, second.calls)
	}

	first = &failingEmitter{err: context.Canceled}
	second = &failingEmitter{}
	if err := (audit.MultiEmitter{first, second}).Emit(ctx, validEvent()); err == nil {
		t.Fatal("first emitter failure must propagate")
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("calls = %d/%d, want 1/0", first.calls, second.calls)
	}
}

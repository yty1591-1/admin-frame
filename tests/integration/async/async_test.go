package async_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// newEvent 构造一条唯一事件；topic 用于隔离用例数据。
func newEvent(t *testing.T, topic, agent string) *outbox.Event {
	t.Helper()
	eventID := fmt.Sprintf("9%d-%s-%d", time.Now().UnixNano(), agent, len(topic))
	event, err := outbox.NewEvent(outbox.NewEventParams{
		Topic: topic,
		Envelope: mq.Envelope{
			EventID:    eventID,
			EventType:  "test.async.event",
			Version:    mq.DefaultEventVersion,
			OccurredAt: time.Now().UTC(),
			Payload:    []byte(fmt.Sprintf(`{"event_id":%q}`, eventID)),
		},
		AggregateType: "async_test",
		AggregateID:   agent,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return event
}

// TestOutboxResumeAfterRestart 验证已提交未投递的事件在 Relay 重启后继续投递且不重投（AC-ENT-03-1）。
func TestOutboxResumeAfterRestart(t *testing.T) {
	database := testkit.MySQL(t)
	store := outbox.NewMySQLStore(database.SQL())
	ctx := context.Background()

	event := newEvent(t, "test.async.resume", "resume")
	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var (
		mu        sync.Mutex
		delivered int
	)
	handler := func(_ context.Context, got *outbox.Event) error {
		if got.EventID != event.EventID {
			return nil
		}
		mu.Lock()
		delivered++
		mu.Unlock()
		return nil
	}
	runOnce := func() {
		relay, err := outbox.NewRelay(outbox.RelayOptions{
			Store:        store,
			Handler:      handler,
			BatchSize:    100,
			PollInterval: 10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewRelay: %v", err)
		}
		if _, err := relay.DrainOnce(ctx); err != nil {
			t.Fatalf("DrainOnce: %v", err)
		}
	}

	// 第一次「重启」续投已提交但未发布的事件。
	runOnce()
	// 第二次「重启」不得重投已成功事件。
	runOnce()

	mu.Lock()
	defer mu.Unlock()
	if delivered != 1 {
		t.Fatalf("delivered = %d, want exactly 1", delivered)
	}
}

// TestOutboxConcurrentReplicasDeliverOnce 验证两个 Relay 并发认领时同一事件只投递一次。
func TestOutboxConcurrentReplicasDeliverOnce(t *testing.T) {
	database := testkit.MySQL(t)
	first := outbox.NewMySQLStore(database.SQL())
	second := outbox.NewMySQLStore(database.SQL())
	ctx := context.Background()

	const total = 20
	topic := "test.async.replicas"
	ids := make(map[string]struct{}, total)
	for i := 0; i < total; i++ {
		event := newEvent(t, topic, fmt.Sprintf("replica-%d", i))
		if err := first.Append(ctx, event); err != nil {
			t.Fatalf("Append: %v", err)
		}
		ids[event.EventID] = struct{}{}
	}

	var (
		mu      sync.Mutex
		counts  = make(map[string]int, total)
		unknown []string
	)
	handler := func(_ context.Context, event *outbox.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := ids[event.EventID]; !ok {
			unknown = append(unknown, event.EventID)
			return nil
		}
		counts[event.EventID]++
		return nil
	}

	newRelay := func(store outbox.Store) *outbox.Relay {
		relay, err := outbox.NewRelay(outbox.RelayOptions{
			Store:     store,
			Handler:   handler,
			BatchSize: total,
		})
		if err != nil {
			t.Fatalf("NewRelay: %v", err)
		}
		return relay
	}

	var wg sync.WaitGroup
	for _, relay := range []*outbox.Relay{newRelay(first), newRelay(second)} {
		wg.Add(1)
		go func(relay *outbox.Relay) {
			defer wg.Done()
			if _, err := relay.DrainOnce(ctx); err != nil {
				t.Errorf("DrainOnce: %v", err)
			}
		}(relay)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for eventID := range ids {
		if counts[eventID] != 1 {
			t.Fatalf("event %s delivered %d times, want exactly 1", eventID, counts[eventID])
		}
	}
	if len(unknown) > 0 {
		t.Fatalf("handler saw unrelated events: %v", unknown)
	}
}

// TestOutboxPermanentFailureWritesDLQ 验证毒消息同事务进入死信表（AC-ENT-03-3）。
func TestOutboxPermanentFailureWritesDLQ(t *testing.T) {
	database := testkit.MySQL(t)
	store := outbox.NewMySQLStore(database.SQL())
	ctx := context.Background()

	event := newEvent(t, "test.async.poison", "poison")
	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}

	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store:     store,
		BatchSize: 100,
		Handler: func(_ context.Context, got *outbox.Event) error {
			if got.EventID != event.EventID {
				return nil
			}
			return retry.Permanent(errors.New("payload cannot be handled"))
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}

	assertOutboxStatus(t, database, event.EventID, outbox.StatusFailed)
	var dlqCount int
	if err := database.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM async_dlq WHERE source_id = ?`, event.EventID).Scan(&dlqCount); err != nil {
		t.Fatalf("count dlq: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("dlq rows = %d, want 1", dlqCount)
	}
}

// TestOutboxRetryExhaustedWritesDLQ 验证退避重试耗尽后转入 dead 并写死信。
func TestOutboxRetryExhaustedWritesDLQ(t *testing.T) {
	database := testkit.MySQL(t)
	store := outbox.NewMySQLStore(database.SQL())
	ctx := context.Background()

	event := newEvent(t, "test.async.exhausted", "exhausted")
	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}

	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store:     store,
		BatchSize: 100,
		Policy:    outbox.RetryPolicy{Base: time.Millisecond, Max: time.Millisecond, MaxAttempts: 2},
		Handler: func(_ context.Context, got *outbox.Event) error {
			if got.EventID != event.EventID {
				return nil
			}
			return errors.New("downstream unavailable")
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("first DrainOnce: %v", err)
	}
	// 跳过退避等待，直接让下一次认领到期。
	if _, err := database.SQL().ExecContext(ctx,
		`UPDATE async_outbox SET next_retry_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Second), event.EventID); err != nil {
		t.Fatalf("expedite retry: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("second DrainOnce: %v", err)
	}

	assertOutboxStatus(t, database, event.EventID, outbox.StatusDead)
	var dlqCount int
	if err := database.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM async_dlq WHERE source_id = ? AND replayed_at IS NULL`,
		event.EventID).Scan(&dlqCount); err != nil {
		t.Fatalf("count dlq: %v", err)
	}
	if dlqCount != 1 {
		t.Fatalf("dlq rows = %d, want 1", dlqCount)
	}
}

// TestOutboxCrossProcessDelivery 验证 API 连接写入的事件可由另一个连接（模拟 worker 进程）投递（AC-ENT-03-4）。
func TestOutboxCrossProcessDelivery(t *testing.T) {
	database := testkit.MySQL(t)
	ctx := context.Background()

	workerDB, err := db.Open(ctx, db.Config{Driver: "mysql", DSN: os.Getenv(testkit.EnvMySQLDSN)})
	if err != nil {
		t.Fatalf("open worker connection: %v", err)
	}
	t.Cleanup(func() { _ = workerDB.Close() })

	apiStore := outbox.NewMySQLStore(database.SQL())
	workerStore := outbox.NewMySQLStore(workerDB.SQL())

	event := newEvent(t, "test.async.cross", "cross")
	if err := apiStore.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var delivered string
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store:     workerStore,
		BatchSize: 100,
		Handler: func(_ context.Context, got *outbox.Event) error {
			if got.EventID == event.EventID {
				delivered = got.EventID
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if delivered != event.EventID {
		t.Fatalf("delivered = %q, want %q", delivered, event.EventID)
	}
	assertOutboxStatus(t, database, event.EventID, outbox.StatusSucceeded)
}

func assertOutboxStatus(t *testing.T, database *db.DB, eventID string, want outbox.Status) {
	t.Helper()
	var status string
	if err := database.SQL().QueryRowContext(context.Background(),
		`SELECT status FROM async_outbox WHERE id = ?`, eventID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if outbox.Status(status) != want {
		t.Fatalf("status = %q, want %q", status, want)
	}
}

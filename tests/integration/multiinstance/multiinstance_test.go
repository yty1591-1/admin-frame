package multiinstance_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/idempotency"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// TestNodeIDUniqueAcrossReplicas 验证不同 NodeID 的副本并发批量生成的 ID 无重复（AC-ENT-02-1）。
func TestNodeIDUniqueAcrossReplicas(t *testing.T) {
	const replicas = 2
	const perReplica = 5000

	nodeIDs := []int64{11, 12}
	generators := make([]id.Generator, replicas)
	for i, nodeID := range nodeIDs {
		nodeID := nodeID
		generator, _, err := platform.NewIDGenerator(platform.IDConfig{
			NodeIDSource: platform.IDSourceConfig,
			NodeID:       &nodeID,
		})
		if err != nil {
			t.Fatalf("NewIDGenerator(%d): %v", nodeID, err)
		}
		generators[i] = generator
	}

	var (
		mu   sync.Mutex
		seen = make(map[int64]struct{}, replicas*perReplica)
		wg   sync.WaitGroup
	)
	for _, generator := range generators {
		wg.Add(1)
		go func(generator id.Generator) {
			defer wg.Done()
			for i := 0; i < perReplica; i++ {
				value, err := generator.Next()
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				mu.Lock()
				if _, exists := seen[value]; exists {
					t.Errorf("duplicate id %d", value)
				}
				seen[value] = struct{}{}
				mu.Unlock()
			}
		}(generator)
	}
	wg.Wait()
	if len(seen) != replicas*perReplica {
		t.Fatalf("generated %d unique ids, want %d", len(seen), replicas*perReplica)
	}
}

// TestCrossReplicaIdempotency 验证同一幂等键在两个副本上只执行一次（AC-ENT-02-2）。
func TestCrossReplicaIdempotency(t *testing.T) {
	client := testkit.Redis(t)
	ctx := context.Background()

	first, err := idempotency.NewRedisStore(client)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	second, err := idempotency.NewRedisStore(client)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	key := fmt.Sprintf("idem-%d", time.Now().UnixNano())
	const fingerprint = "POST|/admin/v1/iam/roles|{\"name\":\"ops\"}"

	var createdCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		store := first
		if i%2 == 1 {
			store = second
		}
		wg.Add(1)
		go func(store idempotency.Store) {
			defer wg.Done()
			record, created, beginErr := store.Begin(ctx, key, fingerprint, time.Minute)
			if beginErr != nil {
				if !errors.Is(beginErr, idempotency.ErrInProgress) {
					t.Errorf("Begin: %v", beginErr)
				}
				return
			}
			if !created {
				return
			}
			createdCount.Add(1)
			if record.State != idempotency.StateInProgress {
				t.Errorf("state = %v, want in progress", record.State)
			}
			if err := store.Complete(ctx, key, fingerprint, 201, nil, []byte(`{"id":"1"}`), time.Minute); err != nil {
				t.Errorf("Complete: %v", err)
			}
		}(store)
	}
	wg.Wait()
	if got := createdCount.Load(); got != 1 {
		t.Fatalf("created = %d, want exactly 1 across replicas", got)
	}

	replayed, created, err := second.Begin(ctx, key, fingerprint, time.Minute)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if created || replayed.State != idempotency.StateCompleted || replayed.StatusCode != http.StatusCreated {
		t.Fatalf("replay = %+v created=%v", replayed, created)
	}
}

// TestSharedRateLimitQuota 验证两副本共享同一令牌桶配额（AC-ENT-02-3）。
func TestSharedRateLimitQuota(t *testing.T) {
	client := testkit.Redis(t)
	cfg := ratelimit.RedisKeyedConfig{RPS: 0.001, Burst: 5, IdleTTL: time.Minute}

	first, err := ratelimit.NewRedisKeyed(client, cfg)
	if err != nil {
		t.Fatalf("NewRedisKeyed: %v", err)
	}
	second, err := ratelimit.NewRedisKeyed(client, cfg)
	if err != nil {
		t.Fatalf("NewRedisKeyed: %v", err)
	}

	key := fmt.Sprintf("rl-%d", time.Now().UnixNano())
	allowed := 0
	for i := 0; i < 6; i++ {
		limiter := first
		if i%2 == 1 {
			limiter = second
		}
		if limiter.Allow(key).Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed = %d, want 5 across replicas", allowed)
	}
}

// TestOutboxRestartNoLossNoDuplicate 验证发布器重启后已完成事件不重投、未完成事件不丢失（AC-ENT-02-4）。
func TestOutboxRestartNoLossNoDuplicate(t *testing.T) {
	database := testkit.MySQL(t)
	ctx := context.Background()
	repo := persistence.NewSQLOutboxRepository(database.SQL())

	now := time.Now().UTC()
	suffix := now.UnixNano()
	ids := []string{
		fmt.Sprintf("91%016d", suffix%1e15+1),
		fmt.Sprintf("91%016d", suffix%1e15+2),
		fmt.Sprintf("91%016d", suffix%1e15+3),
	}
	for _, eventID := range ids {
		event, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
			ID:            eventID,
			Type:          domain.OutboxEventAudit,
			AggregateType: "iam_role",
			AggregateID:   "3001",
			Payload:       []byte(fmt.Sprintf(`{"event_id":%q,"action":"rbac.role.change"}`, eventID)),
			Now:           now,
		})
		if err != nil {
			t.Fatalf("NewOutboxEvent: %v", err)
		}
		if err := repo.Append(ctx, event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	var (
		mu        sync.Mutex
		delivered = make(map[string]int)
	)
	deliver := func(_ context.Context, event *domain.OutboxEvent) error {
		mu.Lock()
		defer mu.Unlock()
		delivered[event.ID]++
		return nil
	}
	options := app.OutboxPublisherOptions{
		Events:    repo,
		Deliver:   deliver,
		Policy:    domain.OutboxRetryPolicy{MaxAttempts: 3},
		BatchSize: 100,
	}

	drainUntilDelivered := func() {
		publisher, err := app.NewOutboxPublisher(options)
		if err != nil {
			t.Fatalf("NewOutboxPublisher: %v", err)
		}
		for attempt := 0; attempt < 10; attempt++ {
			if _, err := publisher.Drain(ctx); err != nil {
				t.Fatalf("Drain: %v", err)
			}
			mu.Lock()
			complete := true
			for _, eventID := range ids {
				if delivered[eventID] == 0 {
					complete = false
				}
			}
			mu.Unlock()
			if complete {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("outbox events were not delivered after retries")
	}

	drainUntilDelivered()
	// 模拟滚动重启：新发布器实例只应看到已成功事件，不得重投。
	drainUntilDelivered()

	mu.Lock()
	defer mu.Unlock()
	for _, eventID := range ids {
		if delivered[eventID] != 1 {
			t.Fatalf("event %s delivered %d times, want exactly 1", eventID, delivered[eventID])
		}
	}
}

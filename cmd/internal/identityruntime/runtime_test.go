package identityruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// cleanupSessionsStub 只实现清理路径调用的方法。
type cleanupSessionsStub struct {
	domain.SessionRepository
	expireCalls int
}

func (s *cleanupSessionsStub) ExpireStale(context.Context, time.Time, int) (domain.SessionExpireResult, error) {
	s.expireCalls++
	return domain.SessionExpireResult{Sessions: 1, Families: 1}, nil
}

// DeleteExpiredBefore 返回满批，模拟仍有积压。
func (s *cleanupSessionsStub) DeleteExpiredBefore(context.Context, time.Time, int) (domain.SessionDeleteResult, error) {
	return domain.SessionDeleteResult{Sessions: 1, Families: 1}, nil
}

// cleanupAttemptsStub 只实现 DeleteBefore。
type cleanupAttemptsStub struct{ domain.LoginAttemptRepository }

func (cleanupAttemptsStub) DeleteBefore(context.Context, time.Time, int) (int, error) { return 1, nil }

// cleanupLockStub 模拟分布式锁获取结果。
type cleanupLockStub struct {
	acquired bool
	calls    int
}

func (l *cleanupLockStub) Acquire(context.Context) (func(context.Context), bool, error) {
	l.calls++
	return func(context.Context) {}, l.acquired, nil
}

// cleanupObserverStub 记录清理结果。
type cleanupObserverStub struct {
	results []app.CleanupRunResult
}

func (o *cleanupObserverStub) OnCleanupRun(_ context.Context, result app.CleanupRunResult) {
	o.results = append(o.results, result)
}

// TestNewFailsFastWithoutDatabase 验证 db.driver 非 memory 时缺失连接直接失败（IDN-P2-01）。
func TestNewFailsFastWithoutDatabase(t *testing.T) {
	cfg := &platform.Config{
		Channel:  "admin",
		Identity: platform.IdentityConfig{Enabled: true},
		DB:       platform.DBConfig{Driver: "mysql"},
	}
	if _, err := New(&platform.Deps{Config: cfg}); err == nil ||
		!strings.Contains(err.Error(), "no database connection is available") {
		t.Fatalf("err=%v want fail-fast without database", err)
	}
}

// TestRunCleanupOnceSkipsWhenLockHeld 验证未获得分布式锁时跳过整轮清理（REL-02）。
func TestRunCleanupOnceSkipsWhenLockHeld(t *testing.T) {
	sessions := &cleanupSessionsStub{}
	cleanup, err := app.NewSessionCleanup(sessions, cleanupAttemptsStub{}, app.RetentionPolicy{
		SessionRetention:      time.Hour,
		LoginAttemptRetention: time.Hour,
		ArchiveRetention:      -time.Hour,
		BatchSize:             1,
		MaxBatchesPerRun:      1,
	}, nil)
	if err != nil {
		t.Fatalf("new cleanup: %v", err)
	}
	observer := &cleanupObserverStub{}
	runCleanupOnce(context.Background(), cleanup, &cleanupLockStub{acquired: false}, observer, nil, time.Now().UTC())
	if sessions.expireCalls != 0 {
		t.Fatalf("cleanup must not run when lock is held: calls=%d", sessions.expireCalls)
	}
	if len(observer.results) != 1 || !observer.results[0].Skipped {
		t.Fatalf("observer results = %+v, want one skipped result", observer.results)
	}

	// 获得锁后必须执行清理并上报成功结果。
	runCleanupOnce(context.Background(), cleanup, &cleanupLockStub{acquired: true}, observer, nil, time.Now().UTC())
	if sessions.expireCalls == 0 {
		t.Fatalf("cleanup must run when lock is acquired")
	}
	if len(observer.results) != 2 || observer.results[1].Skipped {
		t.Fatalf("observer results = %+v, want second result not skipped", observer.results)
	}
}

// TestPasswordPolicyFromConfig 验证配置策略映射与零值默认行为（IDN-P2-01）。
func TestPasswordPolicyFromConfig(t *testing.T) {
	empty := passwordPolicyFromConfig(&platform.Config{})
	fallback := app.DefaultPasswordPolicy()
	if empty != fallback {
		t.Fatalf("policy = %+v, want default %+v", empty, fallback)
	}

	requireComplexity := false
	cfg := &platform.Config{}
	cfg.Identity.Password.MinLength = 10
	cfg.Identity.Password.MaxLength = 64
	cfg.Identity.Password.RequireComplexity = &requireComplexity
	policy := passwordPolicyFromConfig(cfg)
	if policy.MinLength != 10 || policy.MaxLength != 64 || policy.RequireComplexity {
		t.Fatalf("policy = %+v, want 10/64/false", policy)
	}
}

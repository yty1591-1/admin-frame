package app_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// perf01TxTracker 标记当前是否处于数据库事务内。
type perf01TxTracker struct {
	inTx *atomic.Bool
}

func (t *perf01TxTracker) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	t.inTx.Store(true)
	defer t.inTx.Store(false)
	return fn(ctx)
}

// txAwareHasher 记录 Argon2 是否在数据库事务内执行。
type txAwareHasher struct {
	inTx       *atomic.Bool
	hashInTx   atomic.Bool
	verifyInTx atomic.Bool
}

func (h *txAwareHasher) Hash(plain []byte) (string, error) {
	if h.inTx.Load() {
		h.hashInTx.Store(true)
	}
	return fakeHasher{}.Hash(plain)
}

func (h *txAwareHasher) Verify(encoded string, plain []byte) error {
	if h.inTx.Load() {
		h.verifyInTx.Store(true)
	}
	return fakeHasher{}.Verify(encoded, plain)
}

func (h *txAwareHasher) NeedsRehash(string) bool { return false }

func (h *txAwareHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// TestPasswordMutationsRunOutsideTransaction 验证改密与重置密码的 Argon2 均不在事务内（PERF-01）。
func TestPasswordMutationsRunOutsideTransaction(t *testing.T) {
	ctx := context.Background()
	inTx := &atomic.Bool{}
	hasher := &txAwareHasher{inTx: inTx}
	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	f := newAuthFixture(t, domain.ChannelAdmin, func(cfg *app.AuthServiceConfig) {
		cfg.Password = passwordSvc
		cfg.UoW = &perf01TxTracker{inTx: inTx}
	})
	f.seedAccount(t, "acct-perf", "alice", "Old#Password12")
	login := f.login(t, "alice", "Old#Password12")

	svc, err := app.NewAccountService(f.svc)
	if err != nil {
		t.Fatalf("new account service: %v", err)
	}
	if err := svc.ChangePassword(ctx, app.ChangePasswordInput{
		Actor:       app.SessionActor{AccountID: login.AccountID, SessionID: login.SessionID},
		OldPassword: "Old#Password12",
		NewPassword: "New#Password12",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if hasher.verifyInTx.Load() || hasher.hashInTx.Load() {
		t.Fatalf("change password must run Argon2 outside transaction: verify=%v hash=%v",
			hasher.verifyInTx.Load(), hasher.hashInTx.Load())
	}

	if _, err := svc.ResetPassword(ctx, app.AccountOperationInput{
		Actor:           app.SessionActor{AccountID: login.AccountID},
		TargetAccountID: login.AccountID,
	}); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if hasher.hashInTx.Load() || hasher.verifyInTx.Load() {
		t.Fatalf("reset password must run Argon2 outside transaction: verify=%v hash=%v",
			hasher.verifyInTx.Load(), hasher.hashInTx.Load())
	}
}

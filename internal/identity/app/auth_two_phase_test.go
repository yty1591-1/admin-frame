package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/jwt"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// txFlagUoW 记录业务事务是否正在执行。
type txFlagUoW struct {
	active atomic.Bool
}

func (u *txFlagUoW) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	u.active.Store(true)
	defer u.active.Store(false)
	return fn(ctx)
}

// txSensitiveHasher 断言密码校验不得发生在业务事务内。
type txSensitiveHasher struct {
	uow        *txFlagUoW
	verifiedIn atomic.Bool
}

func (h *txSensitiveHasher) Hash(plain []byte) (string, error) { return "spy$" + string(plain), nil }

func (h *txSensitiveHasher) Verify(encoded string, plain []byte) error {
	if h.uow.active.Load() {
		h.verifiedIn.Store(true)
	}
	if encoded != "spy$"+string(plain) {
		return password.ErrMismatch
	}
	return nil
}

func (h *txSensitiveHasher) NeedsRehash(string) bool { return false }

func (h *txSensitiveHasher) Algorithm() password.Algorithm { return password.AlgorithmArgon2id }

// seqIDGen 提供递增 ID。
type seqIDGen struct{ next atomic.Int64 }

func (g *seqIDGen) NextString() (string, error) {
	return time.Now().Format("150405") + "-" + string(rune('0'+g.next.Add(1)%10)), nil
}

// flippingCredRepo 在第 2 次读取时返回被并发修改的哈希，模拟登录期间改密。
type flippingCredRepo struct {
	domain.CredentialRepository
	mu    sync.Mutex
	calls int
}

func (r *flippingCredRepo) FindByAccount(ctx context.Context, accountID string, typ domain.CredentialType) (*domain.Credential, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	credential, err := r.CredentialRepository.FindByAccount(ctx, accountID, typ)
	if err != nil {
		return nil, err
	}
	if call >= 2 {
		clone := *credential
		clone.Hash = "spy$Changed#Passw0rd"
		return &clone, nil
	}
	return credential, nil
}

// TestLoginRejectsCredentialChangedBetweenProbeAndTx 验证事务内复核能拒绝并发改密。
func TestLoginRejectsCredentialChangedBetweenProbeAndTx(t *testing.T) {
	ctx := context.Background()
	uow := &txFlagUoW{}
	hasher := &txSensitiveHasher{uow: uow}
	ids := &seqIDGen{}
	accounts := persistence.NewMemoryAccountRepository()
	baseCredentials := persistence.NewMemoryCredentialRepository()
	credentials := &flippingCredRepo{CredentialRepository: baseCredentials}
	attempts := persistence.NewMemoryLoginAttemptRepository()
	sessions := persistence.NewMemorySessionRepository()

	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "https://identity.test", Audience: []string{"admin"},
		Secret: []byte("01234567890123456789012345678901"), TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, domain.ChannelAdmin, 15*time.Minute, 168*time.Hour, ids)
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	svc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts: accounts, Credentials: credentials, Sessions: sessions,
		Guard: guard, Password: passwordSvc, Tokens: tokens,
		IDs: ids, Channel: domain.ChannelAdmin,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "1", Channel: domain.ChannelAdmin, Subject: "alice", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID: "2", AccountID: account.ID, Type: domain.CredentialTypePassword,
		Hash: "spy$Correct#Passw0rd", Algorithm: "spy", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	if err := baseCredentials.Save(ctx, credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	_, err = svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "Correct#Passw0rd"})
	if err == nil {
		t.Fatal("login must reject credential changed between probe and tx")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeUnauthorized {
		t.Fatalf("error code = %v, want 40100", err)
	}
}

// TestLoginPasswordVerifyOutsideTransaction 验证 Argon2 校验在事务外执行。
func TestLoginPasswordVerifyOutsideTransaction(t *testing.T) {
	ctx := context.Background()
	uow := &txFlagUoW{}
	hasher := &txSensitiveHasher{uow: uow}
	ids := &seqIDGen{}
	accounts := persistence.NewMemoryAccountRepository()
	credentials := persistence.NewMemoryCredentialRepository()
	attempts := persistence.NewMemoryLoginAttemptRepository()
	sessions := persistence.NewMemorySessionRepository()

	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("new password service: %v", err)
	}
	guard, err := app.NewLoginGuard(accounts, attempts, ids, app.DefaultLockoutPolicy())
	if err != nil {
		t.Fatalf("new login guard: %v", err)
	}
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "https://identity.test", Audience: []string{"admin"},
		Secret: []byte("01234567890123456789012345678901"), TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tokens, err := app.NewTokenService(signer, domain.ChannelAdmin, 15*time.Minute, 168*time.Hour, ids)
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	svc, err := app.NewAuthService(app.AuthServiceConfig{
		Accounts: accounts, Credentials: credentials, Sessions: sessions,
		Guard: guard, Password: passwordSvc, Tokens: tokens,
		IDs: ids, Channel: domain.ChannelAdmin, UoW: uow,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	account, err := domain.NewAccount(domain.NewAccountParams{
		ID: "1", Channel: domain.ChannelAdmin, Subject: "alice", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	if err := accounts.Save(ctx, account, 0); err != nil {
		t.Fatalf("save account: %v", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID: "2", AccountID: account.ID, Type: domain.CredentialTypePassword,
		Hash: "spy$Correct#Passw0rd", Algorithm: "spy", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	if err := credentials.Save(ctx, credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	if _, err := svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "Correct#Passw0rd"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if hasher.verifiedIn.Load() {
		t.Fatal("password verification must not run inside the unit of work")
	}

	// 失败路径同样不能在事务内执行 Argon2。
	if _, err := svc.Login(ctx, app.LoginInput{Subject: "alice", Password: "Wrong#Passw0rd"}); err == nil {
		t.Fatal("wrong password must be rejected")
	}
	if hasher.verifiedIn.Load() {
		t.Fatal("failed password verification must not run inside the unit of work")
	}
}

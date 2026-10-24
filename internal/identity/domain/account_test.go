package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// baseTime 是状态机用例的固定时间基准，便于断言锁定期与版本。
var baseTime = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// mustAccount 构造合法账号；mutate 可覆盖入参。
func mustAccount(t *testing.T, mutate func(*NewAccountParams)) *Account {
	t.Helper()
	params := NewAccountParams{
		ID:      "1001",
		Channel: ChannelAdmin,
		Subject: "Alice",
		Now:     baseTime,
	}
	if mutate != nil {
		mutate(&params)
	}
	account, err := NewAccount(params)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func TestNewAccountValidation(t *testing.T) {
	tests := []struct {
		name    string
		params  NewAccountParams
		wantErr error
	}{
		{
			name:   "admin account",
			params: NewAccountParams{ID: "1001", Channel: ChannelAdmin, Subject: " Alice ", Now: baseTime},
		},
		{
			name:   "biz account requires merchant",
			params: NewAccountParams{ID: "1002", Channel: ChannelBiz, Subject: "bob", MerchantID: "m-1", Now: baseTime},
		},
		{
			name:    "unsupported channel",
			params:  NewAccountParams{ID: "1003", Channel: Channel("partner"), Subject: "carol", Now: baseTime},
			wantErr: ErrInvalidChannel,
		},
		{
			name:    "empty subject",
			params:  NewAccountParams{ID: "1004", Channel: ChannelAdmin, Subject: "   ", Now: baseTime},
			wantErr: ErrEmptyInput,
		},
		{
			name:    "empty id",
			params:  NewAccountParams{Channel: ChannelAdmin, Subject: "dave", Now: baseTime},
			wantErr: ErrEmptyInput,
		},
		{
			name:    "biz without merchant",
			params:  NewAccountParams{ID: "1005", Channel: ChannelBiz, Subject: "erin", Now: baseTime},
			wantErr: ErrEmptyInput,
		},
		{
			name:    "admin with merchant",
			params:  NewAccountParams{ID: "1006", Channel: ChannelAdmin, Subject: "frank", MerchantID: "m-1", Now: baseTime},
			wantErr: ErrInvalidAccount,
		},
		{
			name:    "c channel with merchant",
			params:  NewAccountParams{ID: "1007", Channel: ChannelC, Subject: "grace", MerchantID: "m-1", Now: baseTime},
			wantErr: ErrInvalidAccount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewAccount(tt.params)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewAccount() error = %v, want sentinel %v", err, tt.wantErr)
				}
				if !errors.Is(err, apperr.ErrBadRequest) {
					t.Fatalf("NewAccount() error = %v, want apperr.ErrBadRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewAccount() error = %v", err)
			}
			if account.Status != AccountStatusPending || account.TokenVersion != 1 || account.Version != 1 {
				t.Fatalf("new account = status %s, token_version %d, version %d; want pending / 1 / 1",
					account.Status, account.TokenVersion, account.Version)
			}
			if account.Subject != NormalizeSubject(tt.params.Subject) {
				t.Fatalf("subject = %q, want normalized %q", account.Subject, NormalizeSubject(tt.params.Subject))
			}
		})
	}
}

func TestAccountStateTransitions(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(*Account)
		apply            func(*Account) error
		wantStatus       AccountStatus
		wantErr          error
		wantTokenVersion int64
		wantLocked       bool
		wantVersion      int64
	}{
		{
			name:             "pending activates",
			apply:            func(a *Account) error { return a.Activate(baseTime) },
			wantStatus:       AccountStatusActive,
			wantTokenVersion: 1,
			wantVersion:      2,
		},
		{
			name:             "active activation is idempotent",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Activate(baseTime) },
			wantStatus:       AccountStatusActive,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "disabled cannot activate",
			setup:            func(a *Account) { a.Status = AccountStatusDisabled },
			apply:            func(a *Account) error { return a.Activate(baseTime) },
			wantStatus:       AccountStatusDisabled,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "active locks",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Lock(baseTime.Add(15*time.Minute), baseTime) },
			wantStatus:       AccountStatusLocked,
			wantTokenVersion: 1,
			wantLocked:       true,
			wantVersion:      2,
		},
		{
			name:             "pending cannot lock",
			apply:            func(a *Account) error { return a.Lock(baseTime.Add(15*time.Minute), baseTime) },
			wantStatus:       AccountStatusPending,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "lock expiry must be after now",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Lock(baseTime, baseTime) },
			wantStatus:       AccountStatusActive,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name: "locked unlocks",
			setup: func(a *Account) {
				a.Status = AccountStatusLocked
				until := baseTime.Add(15 * time.Minute)
				a.LockedUntil = &until
			},
			apply:            func(a *Account) error { return a.Unlock(baseTime) },
			wantStatus:       AccountStatusActive,
			wantTokenVersion: 1,
			wantLocked:       false,
			wantVersion:      2,
		},
		{
			name:             "active cannot unlock",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Unlock(baseTime) },
			wantStatus:       AccountStatusActive,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "active disables and bumps token version",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Disable(baseTime) },
			wantStatus:       AccountStatusDisabled,
			wantTokenVersion: 2,
			wantVersion:      2,
		},
		{
			name:             "disable is idempotent",
			setup:            func(a *Account) { a.Status = AccountStatusDisabled },
			apply:            func(a *Account) error { return a.Disable(baseTime) },
			wantStatus:       AccountStatusDisabled,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "disabled enables",
			setup:            func(a *Account) { a.Status = AccountStatusDisabled },
			apply:            func(a *Account) error { return a.Enable(baseTime) },
			wantStatus:       AccountStatusActive,
			wantTokenVersion: 1,
			wantVersion:      2,
		},
		{
			name:             "pending cannot enable",
			apply:            func(a *Account) error { return a.Enable(baseTime) },
			wantStatus:       AccountStatusPending,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
		{
			name:             "delete soft deletes and bumps token version",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.Delete(baseTime) },
			wantStatus:       AccountStatusDisabled,
			wantTokenVersion: 2,
			wantVersion:      2,
		},
		{
			name:             "change password bumps token version",
			setup:            func(a *Account) { a.Status = AccountStatusActive },
			apply:            func(a *Account) error { return a.ChangePassword(baseTime) },
			wantStatus:       AccountStatusActive,
			wantTokenVersion: 2,
			wantVersion:      2,
		},
		{
			name:             "pending cannot change password",
			apply:            func(a *Account) error { return a.ChangePassword(baseTime) },
			wantStatus:       AccountStatusPending,
			wantErr:          ErrInvalidAccountTransition,
			wantTokenVersion: 1,
			wantVersion:      1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := mustAccount(t, nil)
			if tt.setup != nil {
				tt.setup(account)
			}
			err := tt.apply(account)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("transition error = %v, want sentinel %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("transition error = %v, want nil", err)
			}
			if account.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", account.Status, tt.wantStatus)
			}
			if account.TokenVersion != tt.wantTokenVersion {
				t.Fatalf("token_version = %d, want %d", account.TokenVersion, tt.wantTokenVersion)
			}
			if account.Version != tt.wantVersion {
				t.Fatalf("version = %d, want %d", account.Version, tt.wantVersion)
			}
			if tt.wantLocked && account.LockedUntil == nil {
				t.Fatal("LockedUntil = nil, want a value")
			}
			if !tt.wantLocked && account.LockedUntil != nil {
				t.Fatalf("LockedUntil = %v, want nil", account.LockedUntil)
			}
		})
	}
}

func TestAccountTryUnlockExpired(t *testing.T) {
	tests := []struct {
		name       string
		status     AccountStatus
		until      *time.Time
		wantChange bool
		wantStatus AccountStatus
	}{
		{
			name:       "expired lock recovers",
			status:     AccountStatusLocked,
			until:      timePtr(baseTime.Add(-time.Minute)),
			wantChange: true,
			wantStatus: AccountStatusActive,
		},
		{
			name:       "active lock keeps locked",
			status:     AccountStatusLocked,
			until:      timePtr(baseTime.Add(time.Minute)),
			wantChange: false,
			wantStatus: AccountStatusLocked,
		},
		{
			name:       "active account unchanged",
			status:     AccountStatusActive,
			wantChange: false,
			wantStatus: AccountStatusActive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := mustAccount(t, nil)
			account.Status = tt.status
			account.LockedUntil = tt.until
			changed, err := account.TryUnlockExpired(baseTime)
			if err != nil {
				t.Fatalf("TryUnlockExpired() error = %v", err)
			}
			if changed != tt.wantChange {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChange)
			}
			if account.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", account.Status, tt.wantStatus)
			}
			if tt.wantChange && account.LockedUntil != nil {
				t.Fatalf("LockedUntil = %v, want nil", account.LockedUntil)
			}
		})
	}
}

func TestAccountCanAuthenticate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Account)
		want  bool
	}{
		{name: "pending can login", want: true},
		{name: "active can login", setup: func(a *Account) { a.Status = AccountStatusActive }, want: true},
		{
			name: "locked rejected",
			setup: func(a *Account) {
				a.Status = AccountStatusLocked
				a.LockedUntil = timePtr(baseTime.Add(time.Minute))
			},
			want: false,
		},
		{
			name: "expired lock can retry",
			setup: func(a *Account) {
				a.Status = AccountStatusLocked
				a.LockedUntil = timePtr(baseTime.Add(-time.Minute))
			},
			want: true,
		},
		{name: "disabled rejected", setup: func(a *Account) { a.Status = AccountStatusDisabled }, want: false},
		{name: "deleted rejected", setup: func(a *Account) { deleted := baseTime; a.DeletedAt = &deleted }, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := mustAccount(t, nil)
			if tt.setup != nil {
				tt.setup(account)
			}
			if got := account.CanAuthenticate(baseTime); got != tt.want {
				t.Fatalf("CanAuthenticate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// timePtr 返回时间指针。
func timePtr(value time.Time) *time.Time { return &value }

package domain

import (
	"errors"
	"testing"
	"time"
)

// mustSession 构造合法会话；mutate 可覆盖入参。
func mustSession(t *testing.T, mutate func(*NewSessionParams)) *Session {
	t.Helper()
	params := NewSessionParams{
		ID:           "2001",
		AccountID:    "1001",
		FamilyID:     "2001",
		RefreshHash:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		TokenVersion: 1,
		ExpiresAt:    baseTime.Add(168 * time.Hour),
		Now:          baseTime,
	}
	if mutate != nil {
		mutate(&params)
	}
	session, err := NewSession(params)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}

// defaultSessionParams 返回合法的最小入参集合。
func defaultSessionParams() NewSessionParams {
	return NewSessionParams{
		ID:           "2001",
		AccountID:    "1001",
		FamilyID:     "2001",
		RefreshHash:  "hash",
		TokenVersion: 1,
		ExpiresAt:    baseTime.Add(time.Hour),
		Now:          baseTime,
	}
}

func TestNewSessionValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*NewSessionParams)
	}{
		{name: "missing id", mutate: func(p *NewSessionParams) { p.ID = "" }},
		{name: "missing account", mutate: func(p *NewSessionParams) { p.AccountID = "" }},
		{name: "missing family", mutate: func(p *NewSessionParams) { p.FamilyID = "" }},
		{name: "missing refresh hash", mutate: func(p *NewSessionParams) { p.RefreshHash = "" }},
		{name: "token version zero", mutate: func(p *NewSessionParams) { p.TokenVersion = 0 }},
		{name: "expiry not after now", mutate: func(p *NewSessionParams) { p.ExpiresAt = baseTime }},
		{
			name:   "idle expiry exceeds absolute expiry",
			mutate: func(p *NewSessionParams) { p.IdleExpiresAt = p.ExpiresAt.Add(time.Hour) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := defaultSessionParams()
			tt.mutate(&params)
			_, err := NewSession(params)
			if !errors.Is(err, ErrEmptyInput) && !errors.Is(err, ErrInvalidSession) {
				t.Fatalf("NewSession() error = %v, want ErrEmptyInput or ErrInvalidSession", err)
			}
		})
	}
}

func TestSessionStateTransitions(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*Session)
		apply      func(*Session, time.Time) error
		wantStatus SessionStatus
		wantErr    error
	}{
		{
			name:       "active rotates",
			apply:      func(s *Session, now time.Time) error { return s.Rotate(now) },
			wantStatus: SessionStatusRotated,
		},
		{
			name:       "rotated cannot rotate again",
			setup:      func(s *Session) { s.Status = SessionStatusRotated },
			apply:      func(s *Session, now time.Time) error { return s.Rotate(now) },
			wantStatus: SessionStatusRotated,
			wantErr:    ErrSessionNotActive,
		},
		{
			name:       "revoked cannot rotate",
			setup:      func(s *Session) { s.Status = SessionStatusRevoked },
			apply:      func(s *Session, now time.Time) error { return s.Rotate(now) },
			wantStatus: SessionStatusRevoked,
			wantErr:    ErrSessionNotActive,
		},
		{
			name:       "expired absolute cannot rotate",
			setup:      func(s *Session) { s.ExpiresAt = baseTime.Add(-time.Minute) },
			apply:      func(s *Session, now time.Time) error { return s.Rotate(now) },
			wantStatus: SessionStatusActive,
			wantErr:    ErrSessionExpired,
		},
		{
			name:       "active revokes",
			apply:      func(s *Session, now time.Time) error { return s.Revoke(RevokeReasonLogout, now) },
			wantStatus: SessionStatusRevoked,
		},
		{
			name:       "rotated revokes for family replay",
			setup:      func(s *Session) { s.Status = SessionStatusRotated },
			apply:      func(s *Session, now time.Time) error { return s.Revoke(RevokeReasonReplay, now) },
			wantStatus: SessionStatusRevoked,
		},
		{
			name:       "revoked is idempotent",
			setup:      func(s *Session) { s.Status = SessionStatusRevoked },
			apply:      func(s *Session, now time.Time) error { return s.Revoke(RevokeReasonLogout, now) },
			wantStatus: SessionStatusRevoked,
		},
		{
			name:       "expired cannot revoke",
			setup:      func(s *Session) { s.Status = SessionStatusExpired },
			apply:      func(s *Session, now time.Time) error { return s.Revoke(RevokeReasonLogout, now) },
			wantStatus: SessionStatusExpired,
			wantErr:    ErrSessionExpired,
		},
		{
			name:       "revoke requires reason",
			apply:      func(s *Session, now time.Time) error { return s.Revoke("", now) },
			wantStatus: SessionStatusActive,
			wantErr:    ErrInvalidSession,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := mustSession(t, nil)
			if tt.setup != nil {
				tt.setup(session)
			}
			err := tt.apply(session, baseTime)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("transition error = %v, want sentinel %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("transition error = %v, want nil", err)
			}
			if session.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", session.Status, tt.wantStatus)
			}
		})
	}
}

func TestSessionIsActive(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Session)
		now    time.Time
		active bool
	}{
		{name: "active", now: baseTime, active: true},
		{
			name:   "absolute expired",
			setup:  func(s *Session) { s.ExpiresAt = baseTime.Add(-time.Second) },
			now:    baseTime,
			active: false,
		},
		{
			name:   "idle expired",
			setup:  func(s *Session) { s.IdleExpiresAt = baseTime.Add(-time.Second) },
			now:    baseTime,
			active: false,
		},
		{
			name:   "rotated",
			setup:  func(s *Session) { s.Status = SessionStatusRotated },
			now:    baseTime,
			active: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := mustSession(t, nil)
			if tt.setup != nil {
				tt.setup(session)
			}
			if got := session.IsActive(tt.now); got != tt.active {
				t.Fatalf("IsActive() = %v, want %v", got, tt.active)
			}
		})
	}
}

func TestSessionExpire(t *testing.T) {
	session := mustSession(t, func(p *NewSessionParams) { p.ExpiresAt = baseTime.Add(time.Minute) })
	if err := session.Expire(baseTime); !errors.Is(err, ErrInvalidSessionStatus) {
		t.Fatalf("Expire(before due) error = %v, want ErrInvalidSessionStatus", err)
	}
	if err := session.Expire(baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("Expire(due) error = %v", err)
	}
	if session.Status != SessionStatusExpired {
		t.Fatalf("status = %s, want expired", session.Status)
	}
	if err := session.Expire(baseTime.Add(2 * time.Minute)); err != nil {
		t.Fatalf("Expire(idempotent) error = %v", err)
	}
}

func TestSessionTouch(t *testing.T) {
	session := mustSession(t, nil)
	if err := session.Touch(baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	if !session.LastUsedAt.Equal(baseTime.Add(time.Minute)) {
		t.Fatalf("LastUsedAt = %v, want %v", session.LastUsedAt, baseTime.Add(time.Minute))
	}
	session.Status = SessionStatusRevoked
	if err := session.Touch(baseTime); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("Touch(revoked) error = %v, want ErrSessionNotActive", err)
	}
}

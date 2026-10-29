package domain

import (
	"errors"
	"strings"
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

// mustSessionFamily 构造合法设备会话；mutate 可覆盖入参。
func mustSessionFamily(t *testing.T, mutate func(*NewSessionFamilyParams)) *SessionFamily {
	t.Helper()
	params := NewSessionFamilyParams{
		ID:                "3001",
		AccountID:         "1001",
		ExpiresAt:         baseTime.Add(time.Hour),
		AbsoluteExpiresAt: baseTime.Add(24 * time.Hour),
		Now:               baseTime,
	}
	if mutate != nil {
		mutate(&params)
	}
	family, err := NewSessionFamily(params)
	if err != nil {
		t.Fatalf("NewSessionFamily() error = %v", err)
	}
	return family
}

func TestNewSessionFamilyValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*NewSessionFamilyParams)
	}{
		{name: "missing id", mutate: func(p *NewSessionFamilyParams) { p.ID = "" }},
		{name: "missing account", mutate: func(p *NewSessionFamilyParams) { p.AccountID = "" }},
		{
			name:   "device label too long",
			mutate: func(p *NewSessionFamilyParams) { p.DeviceLabel = strings.Repeat("x", MaxDeviceLabelLen+1) },
		},
		{name: "expiry not after now", mutate: func(p *NewSessionFamilyParams) { p.ExpiresAt = baseTime }},
		{
			name: "absolute expiry precedes expiry",
			mutate: func(p *NewSessionFamilyParams) {
				p.AbsoluteExpiresAt = p.ExpiresAt.Add(-time.Minute)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := NewSessionFamilyParams{
				ID:                "3001",
				AccountID:         "1001",
				ExpiresAt:         baseTime.Add(time.Hour),
				AbsoluteExpiresAt: baseTime.Add(24 * time.Hour),
				Now:               baseTime,
			}
			tt.mutate(&params)
			_, err := NewSessionFamily(params)
			if !errors.Is(err, ErrEmptyInput) && !errors.Is(err, ErrInvalidSession) {
				t.Fatalf("NewSessionFamily() error = %v, want ErrEmptyInput or ErrInvalidSession", err)
			}
		})
	}
}

func TestNewSessionFamilyDefaultsAbsoluteExpiry(t *testing.T) {
	family := mustSessionFamily(t, func(p *NewSessionFamilyParams) { p.AbsoluteExpiresAt = time.Time{} })
	if !family.AbsoluteExpiresAt.Equal(family.ExpiresAt) {
		t.Fatalf("AbsoluteExpiresAt = %v, want %v", family.AbsoluteExpiresAt, family.ExpiresAt)
	}
}

func TestSessionFamilyIsActive(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*SessionFamily)
		now    time.Time
		active bool
	}{
		{name: "active", now: baseTime, active: true},
		{
			name:   "revoked",
			setup:  func(f *SessionFamily) { f.Status = SessionStatusRevoked },
			now:    baseTime,
			active: false,
		},
		{
			name:   "current expiry reached",
			setup:  func(f *SessionFamily) { f.ExpiresAt = baseTime.Add(-time.Second) },
			now:    baseTime,
			active: false,
		},
		{
			name:   "absolute expiry reached",
			setup:  func(f *SessionFamily) { f.AbsoluteExpiresAt = baseTime.Add(-time.Second) },
			now:    baseTime,
			active: false,
		},
		{
			name:   "historical row without absolute expiry",
			setup:  func(f *SessionFamily) { f.AbsoluteExpiresAt = time.Time{} },
			now:    baseTime,
			active: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			family := mustSessionFamily(t, nil)
			if tt.setup != nil {
				tt.setup(family)
			}
			if got := family.IsActive(tt.now); got != tt.active {
				t.Fatalf("IsActive() = %v, want %v", got, tt.active)
			}
		})
	}
}

func TestSessionFamilyTouch(t *testing.T) {
	family := mustSessionFamily(t, nil)
	if err := family.Touch(baseTime.Add(time.Minute), baseTime.Add(2*time.Hour)); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	if !family.ExpiresAt.Equal(baseTime.Add(2 * time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want %v", family.ExpiresAt, baseTime.Add(2*time.Hour))
	}
	if !family.LastUsedAt.Equal(baseTime.Add(time.Minute)) {
		t.Fatalf("LastUsedAt = %v, want %v", family.LastUsedAt, baseTime.Add(time.Minute))
	}

	// 刷新不得延长设备绝对生命上限。
	if err := family.Touch(baseTime.Add(2*time.Hour), baseTime.Add(48*time.Hour)); err != nil {
		t.Fatalf("Touch(capped) error = %v", err)
	}
	if !family.ExpiresAt.Equal(baseTime.Add(24 * time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want absolute expiry", family.ExpiresAt)
	}

	if err := family.Touch(baseTime.Add(25*time.Hour), baseTime.Add(26*time.Hour)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Touch(beyond absolute) error = %v, want ErrSessionExpired", err)
	}

	family.Status = SessionStatusRevoked
	if err := family.Touch(baseTime, baseTime.Add(time.Hour)); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("Touch(revoked) error = %v, want ErrSessionNotActive", err)
	}

	// 历史数据未记录绝对上限时，本次刷新把过期时间提升为绝对上限。
	legacy := mustSessionFamily(t, nil)
	legacy.AbsoluteExpiresAt = time.Time{}
	if err := legacy.Touch(baseTime.Add(time.Minute), baseTime.Add(2*time.Hour)); err != nil {
		t.Fatalf("Touch(legacy) error = %v", err)
	}
	if !legacy.AbsoluteExpiresAt.Equal(baseTime.Add(2 * time.Hour)) {
		t.Fatalf("AbsoluteExpiresAt = %v, want %v", legacy.AbsoluteExpiresAt, baseTime.Add(2*time.Hour))
	}
}

func TestSessionFamilyRevoke(t *testing.T) {
	family := mustSessionFamily(t, nil)
	if err := family.Revoke(RevokeReasonLogout, baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if family.Status != SessionStatusRevoked {
		t.Fatalf("status = %s, want revoked", family.Status)
	}
	if family.RevokedAt == nil || !family.RevokedAt.Equal(baseTime.Add(time.Minute)) {
		t.Fatalf("RevokedAt = %v", family.RevokedAt)
	}
	if family.RevokedReason != RevokeReasonLogout {
		t.Fatalf("RevokedReason = %s", family.RevokedReason)
	}
	if err := family.Revoke(RevokeReasonAdmin, baseTime); err != nil {
		t.Fatalf("Revoke(idempotent) error = %v", err)
	}

	invalid := mustSessionFamily(t, nil)
	if err := invalid.Revoke("", baseTime); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Revoke(empty reason) error = %v, want ErrInvalidSession", err)
	}

	expired := mustSessionFamily(t, func(p *NewSessionFamilyParams) { p.ExpiresAt = baseTime.Add(time.Minute) })
	expired.Status = SessionStatusExpired
	if err := expired.Revoke(RevokeReasonLogout, baseTime); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Revoke(expired) error = %v, want ErrSessionExpired", err)
	}
}

func TestSessionFamilyExpire(t *testing.T) {
	family := mustSessionFamily(t, nil)
	if err := family.Expire(baseTime); !errors.Is(err, ErrInvalidSessionStatus) {
		t.Fatalf("Expire(not due) error = %v, want ErrInvalidSessionStatus", err)
	}
	if err := family.Expire(baseTime.Add(25 * time.Hour)); err != nil {
		t.Fatalf("Expire(due) error = %v", err)
	}
	if family.Status != SessionStatusExpired {
		t.Fatalf("status = %s, want expired", family.Status)
	}
	if err := family.Expire(baseTime.Add(26 * time.Hour)); err != nil {
		t.Fatalf("Expire(idempotent) error = %v", err)
	}

	revoked := mustSessionFamily(t, nil)
	revoked.Status = SessionStatusRevoked
	if err := revoked.Expire(baseTime); !errors.Is(err, ErrInvalidSessionStatus) {
		t.Fatalf("Expire(revoked) error = %v, want ErrInvalidSessionStatus", err)
	}
}

func TestSessionStatusValidate(t *testing.T) {
	valid := []SessionStatus{
		SessionStatusActive, SessionStatusRotated, SessionStatusRevoked, SessionStatusExpired,
	}
	for _, status := range valid {
		if err := status.Validate(); err != nil {
			t.Fatalf("Validate(%s) error = %v", status, err)
		}
	}
	if err := SessionStatus("pending").Validate(); !errors.Is(err, ErrInvalidSessionStatus) {
		t.Fatalf("Validate(invalid) error = %v, want ErrInvalidSessionStatus", err)
	}
}

func TestSessionEnumString(t *testing.T) {
	if got := SessionStatusRotated.String(); got != "rotated" {
		t.Fatalf("SessionStatus.String() = %q", got)
	}
	if got := RevokeReasonReplay.String(); got != "replay" {
		t.Fatalf("RevokeReason.String() = %q", got)
	}
}

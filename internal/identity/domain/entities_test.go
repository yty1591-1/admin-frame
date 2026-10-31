package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestChannelValidate(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		wantErr error
	}{
		{name: "admin", channel: ChannelAdmin},
		{name: "biz", channel: ChannelBiz},
		{name: "c", channel: ChannelC},
		{name: "unknown", channel: Channel("partner"), wantErr: ErrInvalidChannel},
		{name: "empty", channel: Channel(""), wantErr: ErrInvalidChannel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.channel.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want sentinel %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

// TestNormalizeMerchantID 验证商户 ID 与唯一键口径一致（BIZ-06）。
func TestNormalizeMerchantID(t *testing.T) {
	if got := NormalizeMerchantID("  M-1 "); got != "m-1" {
		t.Fatalf("NormalizeMerchantID() = %q, want m-1", got)
	}
	account, err := NewAccount(NewAccountParams{
		ID: "1001", Channel: ChannelBiz, Subject: "alice", MerchantID: " ACCT-9 ",
	})
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	if account.MerchantID != "acct-9" {
		t.Fatalf("merchant id = %q, want acct-9", account.MerchantID)
	}
}

func TestNormalizeSubject(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trim and lowercase", input: "  Alice@Example.COM ", want: "alice@example.com"},
		{name: "already normalized", input: "bob", want: "bob"},
		{name: "empty", input: "   ", want: ""},
		{name: "unicode untouched", input: " 张三 ", want: "张三"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeSubject(tt.input); got != tt.want {
				t.Fatalf("NormalizeSubject(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// mustCredential 构造合法凭据；mutate 可覆盖入参。
func mustCredential(t *testing.T, mutate func(*NewCredentialParams)) *Credential {
	t.Helper()
	params := NewCredentialParams{
		ID:        "3001",
		AccountID: "1001",
		Type:      CredentialTypePassword,
		Hash:      "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		Algorithm: "argon2id",
		Now:       baseTime,
	}
	if mutate != nil {
		mutate(&params)
	}
	credential, err := NewCredential(params)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	return credential
}

func TestNewCredentialValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*NewCredentialParams)
		wantErr error
	}{
		{name: "valid password", mutate: func(*NewCredentialParams) {}},
		{name: "valid phone", mutate: func(p *NewCredentialParams) {
			p.Type = CredentialTypePhone
			p.Algorithm = "hmac-sha256"
		}},
		{name: "missing id", mutate: func(p *NewCredentialParams) { p.ID = "" }, wantErr: ErrEmptyInput},
		{name: "missing account", mutate: func(p *NewCredentialParams) { p.AccountID = "" }, wantErr: ErrEmptyInput},
		{name: "unknown type", mutate: func(p *NewCredentialParams) { p.Type = CredentialType("email") }, wantErr: ErrInvalidCredentialType},
		{name: "empty hash", mutate: func(p *NewCredentialParams) { p.Hash = " " }, wantErr: ErrInvalidCredential},
		{
			name:    "hash too long",
			mutate:  func(p *NewCredentialParams) { p.Hash = strings.Repeat("x", MaxCredentialHashLen+1) },
			wantErr: ErrInvalidCredential,
		},
		{name: "empty algorithm", mutate: func(p *NewCredentialParams) { p.Algorithm = "" }, wantErr: ErrInvalidCredential},
		{
			name:    "algorithm too long",
			mutate:  func(p *NewCredentialParams) { p.Algorithm = strings.Repeat("x", MaxCredentialAlgorithmLen+1) },
			wantErr: ErrInvalidCredential,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := NewCredentialParams{
				ID:        "3001",
				AccountID: "1001",
				Type:      CredentialTypePassword,
				Hash:      "hash",
				Algorithm: "argon2id",
				Now:       baseTime,
			}
			tt.mutate(&params)
			credential, err := NewCredential(params)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewCredential() error = %v, want sentinel %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewCredential() error = %v", err)
			}
			if !credential.CreatedAt.Equal(baseTime) || !credential.UpdatedAt.Equal(baseTime) {
				t.Fatalf("timestamps = %v / %v, want %v", credential.CreatedAt, credential.UpdatedAt, baseTime)
			}
		})
	}
}

func TestCredentialReplaceHash(t *testing.T) {
	credential := mustCredential(t, nil)
	later := baseTime.Add(time.Hour)
	if err := credential.ReplaceHash("next-hash", "bcrypt", later); err != nil {
		t.Fatalf("ReplaceHash() error = %v", err)
	}
	if credential.Hash != "next-hash" || credential.Algorithm != "bcrypt" {
		t.Fatalf("credential = %+v", credential)
	}
	if !credential.UpdatedAt.Equal(later) {
		t.Fatalf("UpdatedAt = %v, want %v", credential.UpdatedAt, later)
	}
	if !credential.CreatedAt.Equal(baseTime) {
		t.Fatalf("CreatedAt = %v, want unchanged %v", credential.CreatedAt, baseTime)
	}
	if err := credential.ReplaceHash("", "bcrypt", later); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("ReplaceHash(empty) error = %v, want ErrInvalidCredential", err)
	}
}

func TestMFAEnrollmentLifecycle(t *testing.T) {
	secret := []byte("ciphertext")
	enrollment, err := NewMFAEnrollment("1001", secret, baseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	if enrollment.IsConfirmed() {
		t.Fatal("new enrollment must not be confirmed")
	}
	if enrollment.RecoveryCodeHashes == nil {
		t.Fatal("recovery code hashes must be initialized")
	}

	later := baseTime.Add(time.Minute)
	if err := enrollment.Confirm(later); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !enrollment.IsConfirmed() {
		t.Fatal("enrollment must be confirmed")
	}
	if !enrollment.UpdatedAt.Equal(later) {
		t.Fatalf("UpdatedAt = %v, want %v", enrollment.UpdatedAt, later)
	}
	if err := enrollment.Confirm(later); !errors.Is(err, ErrMFAAlreadyConfirmed) {
		t.Fatalf("Confirm(again) error = %v, want ErrMFAAlreadyConfirmed", err)
	}

	if err := enrollment.SetRecoveryCodes([]string{"hash-1", "hash-2"}, later); err != nil {
		t.Fatalf("SetRecoveryCodes() error = %v", err)
	}
	if len(enrollment.RecoveryCodeHashes) != 2 {
		t.Fatalf("recovery codes = %v", enrollment.RecoveryCodeHashes)
	}
}

func TestMFAEnrollmentValidation(t *testing.T) {
	tests := []struct {
		name    string
		account string
		secret  []byte
		wantErr error
	}{
		{name: "valid", account: "1001", secret: []byte("ciphertext")},
		{name: "missing account", account: " ", secret: []byte("ciphertext"), wantErr: ErrEmptyInput},
		{name: "missing secret", account: "1001", secret: nil, wantErr: ErrInvalidMFA},
		{
			name:    "secret too long",
			account: "1001",
			secret:  make([]byte, MaxMFASecretLen+1),
			wantErr: ErrInvalidMFA,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMFAEnrollment(tt.account, tt.secret, baseTime)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewMFAEnrollment() error = %v, want sentinel %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMFAEnrollment() error = %v", err)
			}
		})
	}
}

func TestMFASetRecoveryCodesValidation(t *testing.T) {
	enrollment, err := NewMFAEnrollment("1001", []byte("ciphertext"), baseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	tests := []struct {
		name    string
		hashes  []string
		wantErr error
	}{
		{name: "empty set", hashes: nil, wantErr: ErrInvalidMFA},
		{name: "blank entry", hashes: []string{"ok", " "}, wantErr: ErrInvalidMFA},
		{
			name:    "too long entry",
			hashes:  []string{strings.Repeat("x", MaxRecoveryCodeHashLen+1)},
			wantErr: ErrInvalidMFA,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := enrollment.SetRecoveryCodes(tt.hashes, baseTime); !errors.Is(err, tt.wantErr) {
				t.Fatalf("SetRecoveryCodes() error = %v, want sentinel %v", err, tt.wantErr)
			}
		})
	}
}

func TestMFAEnrollmentTryConsumeStep(t *testing.T) {
	enrollment, err := NewMFAEnrollment("1001", []byte("ciphertext"), baseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	later := baseTime.Add(time.Minute)
	if got := enrollment.TryConsumeStep(42, later); !got {
		t.Fatal("first step must be consumed")
	}
	if enrollment.LastUsedStep != 42 {
		t.Fatalf("LastUsedStep = %d, want 42", enrollment.LastUsedStep)
	}
	if got := enrollment.TryConsumeStep(42, later); got {
		t.Fatal("replayed step must be rejected")
	}
	if got := enrollment.TryConsumeStep(41, later); got {
		t.Fatal("older step must be rejected")
	}
	if got := enrollment.TryConsumeStep(43, later); !got {
		t.Fatal("newer step must be consumed")
	}
	if !enrollment.UpdatedAt.Equal(later) {
		t.Fatalf("UpdatedAt = %v, want %v", enrollment.UpdatedAt, later)
	}
}

func TestMFAEnrollmentRotateSecret(t *testing.T) {
	enrollment, err := NewMFAEnrollment("1001", []byte("old"), baseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	before := enrollment.Version
	later := baseTime.Add(time.Minute)
	if err := enrollment.RotateSecret([]byte("new"), later); err != nil {
		t.Fatalf("RotateSecret() error = %v", err)
	}
	if string(enrollment.SecretCiphertext) != "new" {
		t.Fatalf("SecretCiphertext = %q, want new", enrollment.SecretCiphertext)
	}
	if enrollment.Version != before+1 {
		t.Fatalf("Version = %d, want %d", enrollment.Version, before+1)
	}
	if err := enrollment.RotateSecret(nil, later); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("RotateSecret(empty) error = %v, want ErrInvalidMFA", err)
	}
	if err := enrollment.RotateSecret(make([]byte, MaxMFASecretLen+1), later); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("RotateSecret(too long) error = %v, want ErrInvalidMFA", err)
	}
}

func TestMFAEnrollmentRecoveryCodes(t *testing.T) {
	enrollment, err := NewMFAEnrollment("1001", []byte("ciphertext"), baseTime)
	if err != nil {
		t.Fatalf("NewMFAEnrollment() error = %v", err)
	}
	later := baseTime.Add(time.Minute)
	if err := enrollment.SetRecoveryCodes([]string{"hash-1", "hash-2"}, later); err != nil {
		t.Fatalf("SetRecoveryCodes() error = %v", err)
	}
	if !enrollment.HasRecoveryCode("hash-1") {
		t.Fatal("HasRecoveryCode(hash-1) = false, want true")
	}
	if enrollment.HasRecoveryCode(" ") {
		t.Fatal("HasRecoveryCode(blank) = true, want false")
	}
	if enrollment.HasRecoveryCode("hash-3") {
		t.Fatal("HasRecoveryCode(hash-3) = true, want false")
	}

	if !enrollment.UseRecoveryCode("hash-1", later) {
		t.Fatal("UseRecoveryCode(hash-1) = false, want true")
	}
	if len(enrollment.RecoveryCodeHashes) != 1 || enrollment.RecoveryCodeHashes[0] != "hash-2" {
		t.Fatalf("recovery codes = %v, want [hash-2]", enrollment.RecoveryCodeHashes)
	}
	if enrollment.UseRecoveryCode("hash-1", later) {
		t.Fatal("reused recovery code must be rejected")
	}
	if enrollment.UseRecoveryCode("", later) {
		t.Fatal("blank recovery code must be rejected")
	}
	if !enrollment.UseRecoveryCode("hash-2", later) {
		t.Fatal("UseRecoveryCode(hash-2) = false, want true")
	}
	if enrollment.RecoveryCodeHashes == nil || len(enrollment.RecoveryCodeHashes) != 0 {
		t.Fatalf("recovery codes = %v, want empty non-nil slice", enrollment.RecoveryCodeHashes)
	}
}

func TestAccountStatusValidate(t *testing.T) {
	valid := []AccountStatus{
		AccountStatusPending, AccountStatusActive, AccountStatusLocked, AccountStatusDisabled,
	}
	for _, status := range valid {
		if err := status.Validate(); err != nil {
			t.Fatalf("Validate(%s) error = %v", status, err)
		}
	}
	if err := AccountStatus("expired").Validate(); !errors.Is(err, ErrInvalidAccountStatus) {
		t.Fatalf("Validate(invalid) error = %v, want ErrInvalidAccountStatus", err)
	}
}

func TestEntityEnumString(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "channel", got: ChannelBiz.String(), want: "biz"},
		{name: "account status", got: AccountStatusLocked.String(), want: "locked"},
		{name: "credential type", got: CredentialTypePhone.String(), want: "phone"},
		{name: "login outcome", got: LoginOutcomeDenied.String(), want: "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("String() = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestNewLoginAttemptValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*NewLoginAttemptParams)
		wantErr error
	}{
		{name: "valid failure", mutate: func(*NewLoginAttemptParams) {}},
		{
			name:   "success without reason",
			mutate: func(p *NewLoginAttemptParams) { p.Outcome = LoginOutcomeSuccess; p.Reason = "" },
		},
		{
			// 回归：生产摘要为 `<hash_key_id>:<hex HMAC-SHA256>`，比无前缀的 64 位更长，
			// 长度上限必须容纳 key id 前缀，否则登录成功与失败都无法写入尝试记录。
			name: "keyed digest within limit",
			mutate: func(p *NewLoginAttemptParams) {
				p.AccountRef = "v1:" + strings.Repeat("a", 64)
				p.IPHash = "v1:" + strings.Repeat("b", 64)
				p.UAHash = "v1:" + strings.Repeat("c", 64)
			},
		},
		{name: "missing id", mutate: func(p *NewLoginAttemptParams) { p.ID = "" }, wantErr: ErrEmptyInput},
		{name: "bad channel", mutate: func(p *NewLoginAttemptParams) { p.Channel = "x" }, wantErr: ErrInvalidChannel},
		{name: "missing account ref", mutate: func(p *NewLoginAttemptParams) { p.AccountRef = "" }, wantErr: ErrInvalidLoginAttempt},
		{
			name:    "account ref too long",
			mutate:  func(p *NewLoginAttemptParams) { p.AccountRef = strings.Repeat("x", MaxAccountRefLen+1) },
			wantErr: ErrInvalidLoginAttempt,
		},
		{name: "bad outcome", mutate: func(p *NewLoginAttemptParams) { p.Outcome = "maybe" }, wantErr: ErrInvalidLoginOutcome},
		{
			name:    "reason too long",
			mutate:  func(p *NewLoginAttemptParams) { p.Reason = strings.Repeat("x", MaxLoginReasonLen+1) },
			wantErr: ErrInvalidLoginAttempt,
		},
		{
			name:    "ip hash too long",
			mutate:  func(p *NewLoginAttemptParams) { p.IPHash = strings.Repeat("x", MaxIPHashLen+1) },
			wantErr: ErrInvalidLoginAttempt,
		},
		{
			name:    "ua hash too long",
			mutate:  func(p *NewLoginAttemptParams) { p.UAHash = strings.Repeat("x", MaxUAHashLen+1) },
			wantErr: ErrInvalidLoginAttempt,
		},
		{name: "missing occurred at", mutate: func(p *NewLoginAttemptParams) { p.OccurredAt = time.Time{} }, wantErr: ErrInvalidLoginAttempt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := NewLoginAttemptParams{
				ID:         "4001",
				Channel:    ChannelAdmin,
				AccountRef: "abcdef",
				Outcome:    LoginOutcomeFailure,
				Reason:     LoginReasonBadCredential,
				IPHash:     "iphash",
				UAHash:     "uahash",
				OccurredAt: baseTime,
			}
			tt.mutate(&params)
			attempt, err := NewLoginAttempt(params)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewLoginAttempt() error = %v, want sentinel %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLoginAttempt() error = %v", err)
			}
			if !attempt.OccurredAt.Equal(baseTime) {
				t.Fatalf("OccurredAt = %v, want %v", attempt.OccurredAt, baseTime)
			}
		})
	}
}

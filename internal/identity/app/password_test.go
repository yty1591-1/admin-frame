package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// fastHasher 使用 bcrypt 最低成本，兼顾真实编码与用例速度。
func fastHasher(t *testing.T) *password.Hasher {
	t.Helper()
	hasher, err := password.New(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: 4},
	})
	if err != nil {
		t.Fatalf("password.New() error = %v", err)
	}
	return hasher
}

// errorHasher 用于验证哈希失败的错误映射。
type errorHasher struct {
	hashErr   error
	verifyErr error
}

func (h *errorHasher) Hash([]byte) (string, error) { return "", h.hashErr }
func (h *errorHasher) Verify(string, []byte) error { return h.verifyErr }
func (h *errorHasher) NeedsRehash(string) bool     { return false }
func (h *errorHasher) Algorithm() password.Algorithm {
	return password.AlgorithmBcrypt
}

func TestPasswordPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		policy  PasswordPolicy
		wantErr error
	}{
		{name: "default", policy: DefaultPasswordPolicy()},
		{name: "min boundary", policy: PasswordPolicy{MinLength: 8, MaxLength: 8}},
		{name: "min too small", policy: PasswordPolicy{MinLength: 7, MaxLength: 16}, wantErr: ErrInvalidPasswordPolicy},
		{name: "max below min", policy: PasswordPolicy{MinLength: 12, MaxLength: 11}, wantErr: ErrInvalidPasswordPolicy},
		{
			name:    "max above byte cap",
			policy:  PasswordPolicy{MinLength: 12, MaxLength: password.MaxPasswordBytes + 1},
			wantErr: ErrInvalidPasswordPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
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

func TestPasswordPolicyValidatePassword(t *testing.T) {
	policy := DefaultPasswordPolicy()
	tests := []struct {
		name    string
		plain   string
		wantErr error
	}{
		{name: "compliant", plain: "Str0ng-Passw0rd!"},
		{name: "too short", plain: "Ab1!", wantErr: ErrPasswordTooShort},
		{name: "too long", plain: "Aa1!" + strings.Repeat("x", DefaultPasswordMaxLength), wantErr: ErrPasswordTooLong},
		{name: "less than three classes", plain: "abcdefghijkl", wantErr: ErrPasswordTooWeak},
		{name: "two classes", plain: "abcdefghij12", wantErr: ErrPasswordTooWeak},
		{name: "symbol counts as class", plain: "abcdefghij!@", wantErr: ErrPasswordTooWeak},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.ValidatePassword(tt.plain)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ValidatePassword() error = %v, want sentinel %v", err, tt.wantErr)
				}
				if !errors.Is(err, apperr.ErrBadRequest) {
					t.Fatalf("ValidatePassword() error = %v, want apperr.ErrBadRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidatePassword() error = %v, want nil", err)
			}
		})
	}
}

func TestPasswordPolicyLengthCountsRunes(t *testing.T) {
	policy := PasswordPolicy{MinLength: 4, MaxLength: 4}
	// 4 个中文字符共 12 字节，按字符计数应通过。
	if err := policy.ValidatePassword("密码强度"); err != nil {
		t.Fatalf("ValidatePassword() error = %v, want nil", err)
	}
}

// TestPasswordServiceConcurrencyLimit 验证并发槽位耗尽时等待超时返回 50300（IDN-P2-05）。
func TestPasswordServiceConcurrencyLimit(t *testing.T) {
	service, err := NewPasswordService(fastHasher(t), DefaultPasswordPolicy(),
		WithConcurrencyLimit(1, 50*time.Millisecond))
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	encoded, _, err := service.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	ctx := context.Background()
	release, err := service.acquire(ctx)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	_, err = service.VerifyContext(ctx, encoded, "Str0ng-Passw0rd!")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("VerifyContext() error = %v, want unavailable", err)
	}
	release()

	if _, err := service.VerifyContext(ctx, encoded, "Str0ng-Passw0rd!"); err != nil {
		t.Fatalf("VerifyContext() after release error = %v", err)
	}
}

func TestNewPasswordServiceValidation(t *testing.T) {
	if _, err := NewPasswordService(nil, DefaultPasswordPolicy()); err == nil {
		t.Fatal("NewPasswordService(nil) error = nil, want internal error")
	}

	service, err := NewPasswordService(fastHasher(t), PasswordPolicy{})
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	if service.Policy() != DefaultPasswordPolicy() {
		t.Fatalf("Policy() = %+v, want default", service.Policy())
	}

	if _, err := NewPasswordService(fastHasher(t), PasswordPolicy{MinLength: 4, MaxLength: 8}); err == nil {
		t.Fatal("NewPasswordService(invalid policy) error = nil, want bad request")
	}
}

func TestPasswordServiceHash(t *testing.T) {
	service, err := NewPasswordService(fastHasher(t), DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}

	encoded, algorithm, err := service.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if algorithm != string(password.AlgorithmBcrypt) {
		t.Fatalf("algorithm = %q, want bcrypt", algorithm)
	}
	if !strings.HasPrefix(encoded, "$2") {
		t.Fatalf("encoded = %q, want bcrypt prefix", encoded)
	}

	if _, _, err := service.Hash("weak"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("Hash(weak) error = %v, want ErrPasswordTooShort", err)
	}
}

func TestPasswordServiceHashFailure(t *testing.T) {
	service, err := NewPasswordService(&errorHasher{hashErr: errors.New("boom")}, DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	if _, _, err := service.Hash("Str0ng-Passw0rd!"); err == nil {
		t.Fatal("Hash() error = nil, want internal error")
	}
}

func TestPasswordServiceVerify(t *testing.T) {
	service, err := NewPasswordService(fastHasher(t), DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	encoded, _, err := service.Hash("Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	tests := []struct {
		name       string
		plain      string
		wantErr    error
		wantCode   int
		wantRehash bool
	}{
		{name: "match", plain: "Str0ng-Passw0rd!"},
		{name: "mismatch", plain: "Wrong-Passw0rd!", wantErr: domain.ErrInvalidCredentials, wantCode: apperr.CodeUnauthorized},
		{name: "empty plain", plain: "", wantErr: domain.ErrInvalidCredentials, wantCode: apperr.CodeUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rehash, err := service.Verify(encoded, tt.plain)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Verify() error = %v, want sentinel %v", err, tt.wantErr)
				}
				assertCode(t, err, tt.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}
			if rehash != tt.wantRehash {
				t.Fatalf("needsRehash = %v, want %v", rehash, tt.wantRehash)
			}
		})
	}
}

func TestPasswordServiceVerifyCorruptedHash(t *testing.T) {
	service, err := NewPasswordService(fastHasher(t), DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	_, err = service.Verify("$unknown$hash", "Str0ng-Passw0rd!")
	if err == nil {
		t.Fatal("Verify(corrupted) error = nil, want internal error")
	}
	assertCode(t, err, apperr.CodeInternal)
}

func TestPasswordServiceVerifyNeedsRehash(t *testing.T) {
	// 用较低成本生成哈希，再用较高成本的 Hasher 校验，应提示透明升级。
	low, err := password.New(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: 4},
	})
	if err != nil {
		t.Fatalf("password.New() error = %v", err)
	}
	encoded, err := low.Hash([]byte("Str0ng-Passw0rd!"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	high, err := password.New(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: 5},
	})
	if err != nil {
		t.Fatalf("password.New() error = %v", err)
	}
	service, err := NewPasswordService(high, DefaultPasswordPolicy())
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	rehash, err := service.Verify(encoded, "Str0ng-Passw0rd!")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !rehash {
		t.Fatal("needsRehash = false, want true")
	}
}

// assertCode 断言错误携带的业务码。
func assertCode(t *testing.T, err error, want int) {
	t.Helper()
	var applicationError *apperr.AppError
	if !errors.As(err, &applicationError) {
		t.Fatalf("error = %v, want *apperr.AppError", err)
	}
	if applicationError.Code != want {
		t.Fatalf("code = %d, want %d", applicationError.Code, want)
	}
}

package app

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// 密码策略哨兵错误，可用 errors.Is 匹配。
var (
	// ErrPasswordTooShort 密码长度低于下限。
	ErrPasswordTooShort = errors.New("password too short")
	// ErrPasswordTooLong 密码长度超过上限。
	ErrPasswordTooLong = errors.New("password too long")
	// ErrPasswordTooWeak 密码字符类别不足。
	ErrPasswordTooWeak = errors.New("password too weak")
	// ErrInvalidPasswordPolicy 策略参数自相矛盾。
	ErrInvalidPasswordPolicy = errors.New("invalid password policy")
)

// 默认密码策略，与 docs/identity-authn.md §10 一致。
const (
	// DefaultPasswordMinLength 默认最小长度（字符数）。
	DefaultPasswordMinLength = 12
	// DefaultPasswordMaxLength 默认最大长度（字符数）。
	DefaultPasswordMaxLength = 128
	// passwordMinClasses 复杂度开启时至少覆盖的字符类别数（小写 / 大写 / 数字 / 其他）。
	passwordMinClasses = 3
)

// PasswordPolicy 是密码强度策略；长度按字符（rune）计数，避免多字节字符被误判。
type PasswordPolicy struct {
	// MinLength 最小字符数。
	MinLength int
	// MaxLength 最大字符数；同时受 pkg/password.MaxPasswordBytes 字节上限约束。
	MaxLength int
	// RequireComplexity 是否要求至少三类字符。
	RequireComplexity bool
}

// DefaultPasswordPolicy 返回默认策略：12～128 字符且要求复杂度。
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinLength:         DefaultPasswordMinLength,
		MaxLength:         DefaultPasswordMaxLength,
		RequireComplexity: true,
	}
}

// Validate 校验策略参数自身是否可用（配置加载期使用）。
func (p PasswordPolicy) Validate() error {
	if p.MinLength < 8 {
		return fmt.Errorf("%w: min length %d must be >= 8", ErrInvalidPasswordPolicy, p.MinLength)
	}
	if p.MaxLength < p.MinLength {
		return fmt.Errorf("%w: max length %d must be >= min length %d",
			ErrInvalidPasswordPolicy, p.MaxLength, p.MinLength)
	}
	if p.MaxLength > password.MaxPasswordBytes {
		return fmt.Errorf("%w: max length %d exceeds %d", ErrInvalidPasswordPolicy, p.MaxLength, password.MaxPasswordBytes)
	}
	return nil
}

// ValidatePassword 校验明文密码是否满足策略，返回 40000 参数错误。
func (p PasswordPolicy) ValidatePassword(plain string) error {
	length := utf8.RuneCountInString(plain)
	if length < p.MinLength {
		return apperr.New(apperr.CodeBadRequest,
			fmt.Sprintf("password must be at least %d characters", p.MinLength),
			errors.Join(ErrPasswordTooShort, apperr.ErrBadRequest))
	}
	if length > p.MaxLength || len(plain) > password.MaxPasswordBytes {
		return apperr.New(apperr.CodeBadRequest,
			fmt.Sprintf("password must be at most %d characters", p.MaxLength),
			errors.Join(ErrPasswordTooLong, apperr.ErrBadRequest))
	}
	if p.RequireComplexity && characterClasses(plain) < passwordMinClasses {
		return apperr.New(apperr.CodeBadRequest,
			"password must contain at least three of lowercase, uppercase, digit and symbol",
			errors.Join(ErrPasswordTooWeak, apperr.ErrBadRequest))
	}
	return nil
}

// characterClasses 统计密码覆盖的字符类别数。
func characterClasses(plain string) int {
	var lower, upper, digit, other bool
	for _, r := range plain {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	count := 0
	for _, hit := range []bool{lower, upper, digit, other} {
		if hit {
			count++
		}
	}
	return count
}

// PasswordHasher 是密码哈希的消费侧端口（S-07）。
//
// pkg/password.Hasher 直接满足该接口；测试可注入确定性实现。
type PasswordHasher interface {
	// Hash 哈希明文，返回可持久化的编码串。
	Hash(plain []byte) (string, error)
	// Verify 校验编码哈希与明文。
	Verify(encoded string, plain []byte) error
	// NeedsRehash 报告编码哈希是否低于当前参数，供登录成功后透明升级。
	NeedsRehash(encoded string) bool
	// Algorithm 返回新哈希使用的算法标识。
	Algorithm() password.Algorithm
}

// PasswordService 组合密码策略与哈希器，供登录 / 改密用例调用。
type PasswordService struct {
	hasher PasswordHasher
	policy PasswordPolicy
}

// NewPasswordService 构造密码服务；哈希器必填，策略零值时使用默认策略。
func NewPasswordService(hasher PasswordHasher, policy PasswordPolicy) (*PasswordService, error) {
	if hasher == nil {
		return nil, apperr.Internal("password hasher is required", nil)
	}
	if policy == (PasswordPolicy{}) {
		policy = DefaultPasswordPolicy()
	}
	if err := policy.Validate(); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	return &PasswordService{hasher: hasher, policy: policy}, nil
}

// Policy 返回当前生效的策略。
func (s *PasswordService) Policy() PasswordPolicy { return s.policy }

// Hash 校验强度后哈希明文，返回编码串与算法标识。
func (s *PasswordService) Hash(plain string) (encoded, algorithm string, err error) {
	if err := s.policy.ValidatePassword(plain); err != nil {
		return "", "", err
	}
	encoded, err = s.hasher.Hash([]byte(plain))
	if err != nil {
		return "", "", apperr.Internal("password hashing failed", err)
	}
	return encoded, string(s.hasher.Algorithm()), nil
}

// Verify 校验明文与编码哈希；needsRehash 为 true 时调用方应在成功路径透明升级。
//
// 不匹配、空密码与超长明文统一返回 40100「invalid credentials」，
// 避免通过错误文案或状态码差异枚举账号。哈希格式损坏属于服务端数据问题，返回 50000。
func (s *PasswordService) Verify(encoded, plain string) (needsRehash bool, err error) {
	if err := s.hasher.Verify(encoded, []byte(plain)); err != nil {
		switch {
		case errors.Is(err, password.ErrMismatch),
			errors.Is(err, password.ErrEmptyPassword),
			errors.Is(err, password.ErrPasswordTooLong):
			return false, invalidCredentials()
		default:
			return false, apperr.Internal("password verification failed", err)
		}
	}
	return s.hasher.NeedsRehash(encoded), nil
}

// invalidCredentials 构造统一认证失败错误（40100），真实原因由领域哨兵在进程内区分。
func invalidCredentials() error {
	return apperr.New(apperr.CodeUnauthorized, "invalid credentials",
		errors.Join(domain.ErrInvalidCredentials, apperr.ErrUnauthorized))
}

// normalizeReason 为空原因提供默认值，避免审计出现空原因。
func normalizeReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return domain.LoginReasonBadCredential
	}
	return reason
}

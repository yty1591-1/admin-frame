package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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

// dummyPassword 是时序对齐用的固定占位明文，不代表任何真实凭据（IDN-P2-04）。
const dummyPassword = "identity-timing-equalizer-not-a-real-credential"

// defaultPasswordWaitTimeout 是获取并发槽位的默认等待上限。
const defaultPasswordWaitTimeout = 2 * time.Second

// PasswordServiceOption 配置密码服务的可选能力。
type PasswordServiceOption func(*PasswordService)

// WithConcurrencyLimit 限制同时执行的 Argon2 哈希 / 校验数量（IDN-P2-05）。
//
// maxConcurrent ≤ 0 时不启用；waitTimeout ≤ 0 时使用默认 2s，获取槽位超时返回 50300。
func WithConcurrencyLimit(maxConcurrent int, waitTimeout time.Duration) PasswordServiceOption {
	return func(s *PasswordService) {
		if maxConcurrent <= 0 {
			return
		}
		if waitTimeout <= 0 {
			waitTimeout = defaultPasswordWaitTimeout
		}
		s.slots = make(chan struct{}, maxConcurrent)
		s.waitTimeout = waitTimeout
	}
}

// PasswordService 组合密码策略与哈希器，供登录 / 改密用例调用。
type PasswordService struct {
	hasher      PasswordHasher
	policy      PasswordPolicy
	dummyHash   string
	slots       chan struct{}
	waitTimeout time.Duration
}

// NewPasswordService 构造密码服务；哈希器必填，策略零值时使用默认策略。
//
// 构造时预计算占位哈希，使账号不存在 / 无凭据分支的耗时与真实校验一致。
func NewPasswordService(hasher PasswordHasher, policy PasswordPolicy, opts ...PasswordServiceOption) (*PasswordService, error) {
	if hasher == nil {
		return nil, apperr.Internal("password hasher is required", nil)
	}
	if policy == (PasswordPolicy{}) {
		policy = DefaultPasswordPolicy()
	}
	if err := policy.Validate(); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	dummyHash, dummyErr := hasher.Hash([]byte(dummyPassword))
	if dummyErr != nil {
		// 不阻断启动；VerifyDummy 在无可用占位哈希时退化为空操作。
		dummyHash = ""
	}
	service := &PasswordService{hasher: hasher, policy: policy, dummyHash: dummyHash}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	return service, nil
}

// VerifyContext 在并发槽位内校验密码，超时返回 50300（IDN-P2-05）。
func (s *PasswordService) VerifyContext(ctx context.Context, encoded, plain string) (bool, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return s.Verify(encoded, plain)
}

// VerifyDummyContext 在并发槽位内执行占位校验；超时返回 50300。
func (s *PasswordService) VerifyDummyContext(ctx context.Context) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	s.VerifyDummy()
	return nil
}

// VerifyDummy 对固定占位哈希执行一次真实校验，用于账号不存在、无密码凭据或状态拒绝时对齐耗时。
//
// 结果始终忽略；调用方仍返回统一 invalidCredentials()。
func (s *PasswordService) VerifyDummy() {
	if s == nil || s.dummyHash == "" {
		return
	}
	_ = s.hasher.Verify(s.dummyHash, []byte(dummyPassword+"-probe"))
}

// acquire 获取一个密码计算槽位；未启用并发限制时为空操作。
//
// 排队超过 waitTimeout 或请求取消时返回 50300，避免排队雪崩。
func (s *PasswordService) acquire(ctx context.Context) (func(), error) {
	if s == nil || s.slots == nil {
		return func() {}, nil
	}
	timer := time.NewTimer(s.waitTimeout)
	defer timer.Stop()
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-timer.C:
		return nil, apperr.Unavailable("password verification is busy")
	case <-ctx.Done():
		return nil, apperr.New(apperr.CodeServiceUnavailable, "password verification canceled",
			errors.Join(apperr.ErrUnavailable, ctx.Err()))
	}
}

// Policy 返回当前生效的策略。
func (s *PasswordService) Policy() PasswordPolicy { return s.policy }

// HashContext 在并发槽位内校验强度并哈希明文，超时返回 50300。
//
// 所有生产写路径（改密、重置密码、强制改密、登录重哈希）都必须使用本方法，
// 保证 Argon2 受 max_concurrent 约束，且不占用数据库事务（IDN-GAP-PERF-01）。
func (s *PasswordService) HashContext(ctx context.Context, plain string) (encoded, algorithm string, err error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return "", "", err
	}
	defer release()
	return s.Hash(plain)
}

// HashContextWithPolicy 使用调用方指定的策略校验并哈希（BIZ-04 租户覆盖）。
//
// 策略零值回退当前全局策略；并发槽位与非并发约束与 HashContext 一致。
func (s *PasswordService) HashContextWithPolicy(
	ctx context.Context,
	policy PasswordPolicy,
	plain string,
) (encoded, algorithm string, err error) {
	if policy == (PasswordPolicy{}) {
		policy = s.policy
	}
	if err := policy.Validate(); err != nil {
		return "", "", apperr.Internal("invalid password policy", err)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return "", "", err
	}
	defer release()
	if err := policy.ValidatePassword(plain); err != nil {
		return "", "", err
	}
	encoded, err = s.hasher.Hash([]byte(plain))
	if err != nil {
		return "", "", apperr.Internal("password hashing failed", err)
	}
	return encoded, string(s.hasher.Algorithm()), nil
}

// Hash 校验强度后哈希明文，返回编码串与算法标识。
//
// 仅供装配期预热与非并发场景使用；请求路径请使用 HashContext。
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

package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// 短信验证码参数（docs/identity-authn.md §8.1）。
const (
	// smsCodeLength 验证码位数。
	smsCodeLength = 6
	// smsCodeModulus 生成验证码的取值上界。
	smsCodeModulus = 1000000
)

// e164Pattern 校验 E.164 手机号：+ 加国家码与号码，总长 3-16 位数字。
var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{2,14}$`)

// smsCodePattern 校验 6 位数字验证码。
var smsCodePattern = regexp.MustCompile(`^\d{6}$`)

// SMSCodeStore 是验证码与错误次数的持久化端口。
type SMSCodeStore interface {
	// Save 写入验证码摘要；同手机号覆盖旧值并重置 TTL。
	Save(ctx context.Context, phoneHash, codeHash string, ttl time.Duration) error
	// Get 返回验证码摘要；不存在返回 apperr.NotFound。
	Get(ctx context.Context, phoneHash string) (string, error)
	// Delete 删除验证码；不存在也返回成功。
	Delete(ctx context.Context, phoneHash string) error
	// IncrAttempts 原子递增错误次数并返回当前值。
	IncrAttempts(ctx context.Context, phoneHash string, ttl time.Duration) (int, error)
	// ResetAttempts 清除错误次数。
	ResetAttempts(ctx context.Context, phoneHash string) error
}

// SMSRateLimiter 是短信发送防刷端口。
type SMSRateLimiter interface {
	// AllowResend 校验最小重发间隔；被拒绝时返回建议等待时长。
	AllowResend(ctx context.Context, phoneHash string, interval time.Duration) (bool, time.Duration, error)
	// AllowDaily 校验单手机号日发送上限。
	AllowDaily(ctx context.Context, phoneHash string, limit int) (bool, error)
	// AllowHourlyIP 校验单 IP 小时发送上限。
	AllowHourlyIP(ctx context.Context, ipHash string, limit int) (bool, error)
}

// CodeSender 是短信发送端口，由 infrastructure/sms 适配 pkg/sms。
type CodeSender interface {
	SendCode(ctx context.Context, phone, code string) error
}

// SMSLoginConfig 是短信登录用例的装配参数。
type SMSLoginConfig struct {
	Accounts       domain.AccountRepository
	Guard          *LoginGuard
	Codes          SMSCodeStore
	Limiter        SMSRateLimiter
	Sender         CodeSender
	Auth           *AuthService
	CodeTTL        time.Duration
	ResendInterval time.Duration
	MaxAttempts    int
	DailyPerPhone  int
	HourlyPerIP    int
}

// SendSMSCodeInput 是发送验证码入参。
type SendSMSCodeInput struct {
	// Phone E.164 手机号；只在本层短暂存在，禁止写日志。
	Phone string
	// IPHash 客户端 IP 摘要；由 transport 计算。
	IPHash string
}

// SMSLoginInput 是短信验证码登录入参。
type SMSLoginInput struct {
	// Phone E.164 手机号。
	Phone string
	// Code 6 位验证码。
	Code string
	// IPHash / UAHash 客户端摘要；由 transport 计算。
	IPHash string
	UAHash string
	// TraceID 请求关联 ID。
	TraceID string
}

// SMSLoginService 编排验证码发送、防刷校验与短信登录（docs/identity-authn.md §8.1）。
type SMSLoginService struct {
	accounts       domain.AccountRepository
	guard          *LoginGuard
	codes          SMSCodeStore
	limiter        SMSRateLimiter
	sender         CodeSender
	auth           *AuthService
	codeTTL        time.Duration
	resendInterval time.Duration
	maxAttempts    int
	dailyPerPhone  int
	hourlyPerIP    int
}

// NewSMSLoginService 构造短信登录服务；依赖缺失或参数越界时返回错误。
func NewSMSLoginService(cfg SMSLoginConfig) (*SMSLoginService, error) {
	if cfg.Accounts == nil || cfg.Guard == nil || cfg.Codes == nil ||
		cfg.Limiter == nil || cfg.Sender == nil || cfg.Auth == nil {
		return nil, apperr.Internal("sms login dependencies are required", nil)
	}
	if cfg.CodeTTL <= 0 || cfg.ResendInterval <= 0 || cfg.ResendInterval > cfg.CodeTTL {
		return nil, apperr.BadRequest("sms code ttl and resend interval are invalid")
	}
	if cfg.MaxAttempts < 1 || cfg.DailyPerPhone < 1 || cfg.HourlyPerIP < 1 {
		return nil, apperr.BadRequest("sms limits must be >= 1")
	}
	return &SMSLoginService{
		accounts:       cfg.Accounts,
		guard:          cfg.Guard,
		codes:          cfg.Codes,
		limiter:        cfg.Limiter,
		sender:         cfg.Sender,
		auth:           cfg.Auth,
		codeTTL:        cfg.CodeTTL,
		resendInterval: cfg.ResendInterval,
		maxAttempts:    cfg.MaxAttempts,
		dailyPerPhone:  cfg.DailyPerPhone,
		hourlyPerIP:    cfg.HourlyPerIP,
	}, nil
}

// Send 生成并发送验证码；响应与账号是否存在无关，避免手机号枚举。
func (s *SMSLoginService) Send(ctx context.Context, in SendSMSCodeInput) error {
	phone := strings.TrimSpace(in.Phone)
	if !e164Pattern.MatchString(phone) {
		return apperr.BadRequest("phone must be in E.164 format")
	}
	phoneHash := HashAccountRef(domain.ChannelC, phone)

	allowed, retryAfter, err := s.limiter.AllowResend(ctx, phoneHash, s.resendInterval)
	if err != nil {
		return unavailableSMS()
	}
	if !allowed {
		return tooManySMS("sms code requested too frequently", retryAfter)
	}
	if ok, dailyErr := s.limiter.AllowDaily(ctx, phoneHash, s.dailyPerPhone); dailyErr != nil {
		return unavailableSMS()
	} else if !ok {
		return tooManySMS("sms daily limit exceeded", 0)
	}
	if ipHash := strings.TrimSpace(in.IPHash); ipHash != "" {
		if ok, ipErr := s.limiter.AllowHourlyIP(ctx, ipHash, s.hourlyPerIP); ipErr != nil {
			return unavailableSMS()
		} else if !ok {
			return tooManySMS("sms hourly limit exceeded", 0)
		}
	}

	code, err := generateSMSCode()
	if err != nil {
		return unavailableSMS()
	}
	if err := s.codes.Save(ctx, phoneHash, hashSMSCode(phoneHash, code), s.codeTTL); err != nil {
		return unavailableSMS()
	}
	if err := s.codes.ResetAttempts(ctx, phoneHash); err != nil {
		_ = s.codes.Delete(ctx, phoneHash)
		return unavailableSMS()
	}
	if err := s.sender.SendCode(ctx, phone, code); err != nil {
		// 发送失败时删除已写入的验证码，避免「没收到但可用」。
		_ = s.codes.Delete(ctx, phoneHash)
		return unavailableSMS()
	}
	return nil
}

// Login 校验验证码并完成登录；任何失败分支都返回统一 40100 文案。
func (s *SMSLoginService) Login(ctx context.Context, in SMSLoginInput) (LoginResult, error) {
	phone := strings.TrimSpace(in.Phone)
	code := strings.TrimSpace(in.Code)
	if !e164Pattern.MatchString(phone) || !smsCodePattern.MatchString(code) {
		return LoginResult{}, apperr.BadRequest("phone or code format is invalid")
	}
	phoneHash := HashAccountRef(domain.ChannelC, phone)
	if err := s.guard.CheckRateLimit(loginRateKey(domain.ChannelC, phoneHash, in.IPHash)); err != nil {
		return LoginResult{}, err
	}

	storedHash, err := s.codes.Get(ctx, phoneHash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return LoginResult{}, smsInvalidCode()
		}
		return LoginResult{}, unavailableSMS()
	}
	expectedHash := hashSMSCode(phoneHash, code)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) != 1 {
		attempts, incrErr := s.codes.IncrAttempts(ctx, phoneHash, s.codeTTL)
		if incrErr != nil {
			return LoginResult{}, unavailableSMS()
		}
		if attempts >= s.maxAttempts {
			// 达到上限直接作废验证码，强制重新发送。
			_ = s.codes.Delete(ctx, phoneHash)
			_ = s.codes.ResetAttempts(ctx, phoneHash)
		}
		s.auth.RecordExternalLoginFailure(ctx, phoneHash, domain.LoginReasonBadCredential, in.TraceID)
		return LoginResult{}, smsInvalidCode()
	}
	_ = s.codes.Delete(ctx, phoneHash)
	_ = s.codes.ResetAttempts(ctx, phoneHash)

	account, err := s.accounts.FindBySubject(ctx, domain.ChannelC, phoneSubject(phoneHash))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// D3：未注册手机号不自动建号，保持与错误验证码一致的响应。
			s.auth.RecordExternalLoginFailure(ctx, phoneHash, domain.LoginReasonBadCredential, in.TraceID)
			return LoginResult{}, smsInvalidCode()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}
	return s.auth.CompleteVerifiedLogin(ctx, VerifiedLoginInput{
		AccountID: account.ID,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		TraceID:   in.TraceID,
	})
}

// RecordExternalLoginFailure 记录外部登录失败审计；actor 使用不可逆账号摘要。
func (s *AuthService) RecordExternalLoginFailure(ctx context.Context, accountRef, reason, traceID string) {
	_ = s.emitAudit(ctx, audit.ActionAuthLoginFailed, accountRef, "", "account", accountRef,
		audit.OutcomeFailure, reason, traceID, nil)
}

// phoneSubject 返回手机号在身份域的登录主体：摘要前缀 + 哈希，库内不落明文。
func phoneSubject(phoneHash string) string {
	return "phone:" + phoneHash
}

// hashSMSCode 返回验证码摘要；绑定手机号摘要防止跨号码复用。
func hashSMSCode(phoneHash, code string) string {
	sum := sha256.Sum256([]byte("sms:" + phoneHash + ":" + code))
	return hex.EncodeToString(sum[:])
}

// generateSMSCode 生成 6 位数字验证码（含前导零）。
func generateSMSCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(smsCodeModulus))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", smsCodeLength, value.Int64()), nil
}

// smsInvalidCode 返回与账号不存在 / 验证码错误统一的 40100 文案。
func smsInvalidCode() error {
	return apperr.Unauthorized("invalid verification code")
}

// unavailableSMS 返回依赖不可用的 50300（失败关闭）。
func unavailableSMS() error {
	return apperr.Unavailable("sms service unavailable")
}

// tooManySMS 返回 42900；有建议等待时长时写入文案。
func tooManySMS(message string, retryAfter time.Duration) error {
	if retryAfter > 0 {
		message = fmt.Sprintf("%s, retry after %s", message, retryAfter.Round(time.Second))
	}
	return apperr.TooManyRequests(message)
}

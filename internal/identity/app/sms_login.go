package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/admin-frame/admin-frame/pkg/crypto"
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
	// AllowGlobalDaily 校验全局日发送预算；limit≤0 表示不限制。
	AllowGlobalDaily(ctx context.Context, limit int) (bool, error)
	// ResetResend 回滚一次重发窗口占用（发送失败时允许用户立即重试）。
	ResetResend(ctx context.Context, phoneHash string) error
}

// CodeSender 是短信发送端口，由 infrastructure/sms 适配 pkg/sms。
type CodeSender interface {
	SendCode(ctx context.Context, phone, code string) error
}

// SMSLoginConfig 是短信登录用例的装配参数。
type SMSLoginConfig struct {
	Accounts domain.AccountRepository
	Guard    *LoginGuard
	Codes    SMSCodeStore
	Limiter  SMSRateLimiter
	Sender   CodeSender
	Auth     *AuthService
	// Hasher 是手机号 / 验证码摘要器；nil 时回退历史算法（仅限本地/tests）。
	Hasher *crypto.Hasher
	// Metrics 是核心指标端口；可为 nil。
	Metrics        Metrics
	CodeTTL        time.Duration
	ResendInterval time.Duration
	MaxAttempts    int
	DailyPerPhone  int
	HourlyPerIP    int
	// DailyBudget 全局日发送预算；0 表示不限制。
	DailyBudget int
}

// SendSMSCodeInput 是发送验证码入参。
type SendSMSCodeInput struct {
	// Phone E.164 手机号；只在本层短暂存在，禁止写日志。
	Phone string
	// IPHash 客户端 IP 摘要；由 transport 计算。
	IPHash string
	// TraceID 请求关联 ID；用于审计。
	TraceID string
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
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生。
	Device domain.DeviceInfo
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
	hasher         *crypto.Hasher
	metrics        Metrics
	codeTTL        time.Duration
	resendInterval time.Duration
	maxAttempts    int
	dailyPerPhone  int
	hourlyPerIP    int
	dailyBudget    int
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
		hasher:         cfg.Hasher,
		metrics:        cfg.Metrics,
		codeTTL:        cfg.CodeTTL,
		resendInterval: cfg.ResendInterval,
		maxAttempts:    cfg.MaxAttempts,
		dailyPerPhone:  cfg.DailyPerPhone,
		hourlyPerIP:    cfg.HourlyPerIP,
		dailyBudget:    cfg.DailyBudget,
	}, nil
}

// Send 生成并发送验证码；响应与账号是否存在无关，避免手机号枚举。
func (s *SMSLoginService) Send(ctx context.Context, in SendSMSCodeInput) (err error) {
	defer func() {
		if s.metrics == nil {
			return
		}
		result := MetricResultSuccess
		if err != nil {
			result = MetricResultFailure
		}
		s.metrics.ObserveSMSSend(ctx, result)
	}()
	phone := strings.TrimSpace(in.Phone)
	if !e164Pattern.MatchString(phone) {
		return apperr.BadRequest("phone must be in E.164 format")
	}
	phoneHash := PhoneRef(s.hasher, phone)

	allowed, retryAfter, err := s.limiter.AllowResend(ctx, phoneHash, s.resendInterval)
	if err != nil {
		return unavailableSMS()
	}
	if !allowed {
		s.auditSMSDenied(ctx, phoneHash, in.TraceID, "sms_resend_limited")
		return tooManySMS("sms code requested too frequently", retryAfter)
	}
	sent := false
	defer func() {
		if !sent {
			// 发送失败回滚重发窗口，允许用户立即重试。
			_ = s.limiter.ResetResend(ctx, phoneHash)
		}
	}()
	if ok, dailyErr := s.limiter.AllowDaily(ctx, phoneHash, s.dailyPerPhone); dailyErr != nil {
		return unavailableSMS()
	} else if !ok {
		s.auditSMSDenied(ctx, phoneHash, in.TraceID, "sms_daily_limited")
		return tooManySMS("sms daily limit exceeded", 0)
	}
	if ipHash := strings.TrimSpace(in.IPHash); ipHash != "" {
		if ok, ipErr := s.limiter.AllowHourlyIP(ctx, ipHash, s.hourlyPerIP); ipErr != nil {
			return unavailableSMS()
		} else if !ok {
			s.auditSMSDenied(ctx, phoneHash, in.TraceID, "sms_ip_limited")
			return tooManySMS("sms hourly limit exceeded", 0)
		}
	}
	if s.dailyBudget > 0 {
		if ok, budgetErr := s.limiter.AllowGlobalDaily(ctx, s.dailyBudget); budgetErr != nil {
			return unavailableSMS()
		} else if !ok {
			s.auditSMSDenied(ctx, phoneHash, in.TraceID, "sms_budget_exhausted")
			return tooManySMS("sms daily budget exhausted", 0)
		}
	}

	code, err := generateSMSCode()
	if err != nil {
		return unavailableSMS()
	}
	if err := s.codes.Save(ctx, phoneHash, SMSCodeDigest(s.hasher, phoneHash, code), s.codeTTL); err != nil {
		return unavailableSMS()
	}
	if err := s.codes.ResetAttempts(ctx, phoneHash); err != nil {
		_ = s.codes.Delete(ctx, phoneHash)
		return unavailableSMS()
	}
	// 先落审计再发送：审计不可用时不发送验证码，避免「发出但无审计」。
	if err := s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthSMS, phoneHash, "",
		"phone_ref", phoneHash, audit.OutcomeSuccess, "sms_code_issued", in.TraceID, nil); err != nil {
		_ = s.codes.Delete(ctx, phoneHash)
		return unavailableSMS()
	}
	if err := s.sender.SendCode(ctx, phone, code); err != nil {
		// 发送失败时删除已写入的验证码，避免「没收到但可用」。
		_ = s.codes.Delete(ctx, phoneHash)
		_ = s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthSMS, phoneHash, "",
			"phone_ref", phoneHash, audit.OutcomeFailure, "sms_send_failed", in.TraceID, nil)
		return unavailableSMS()
	}
	sent = true
	return nil
}

// auditSMSDenied 记录一次短信限流拒绝；审计失败不改变 429 结果。
func (s *SMSLoginService) auditSMSDenied(ctx context.Context, phoneHash, traceID, reason string) {
	if s.auth == nil {
		return
	}
	_ = s.auth.emitAuditAs(ctx, "anonymous", audit.ActionAuthRateLimited, phoneHash, "",
		"phone_ref", phoneHash, audit.OutcomeDenied, reason, traceID, nil)
}

// Login 校验验证码并完成登录；任何失败分支都返回统一 40100 文案。
func (s *SMSLoginService) Login(ctx context.Context, in SMSLoginInput) (LoginResult, error) {
	phoneHash, err := s.verifyInput(in)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.consumeCode(ctx, phoneHash, strings.TrimSpace(in.Code), in.TraceID); err != nil {
		return LoginResult{}, err
	}

	account, err := s.accounts.FindBySubject(ctx, domain.ChannelC, "", phoneSubject(phoneHash))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// D3：未注册手机号不自动建号，保持与错误验证码一致的响应。
			if auditErr := s.auth.RecordExternalLoginFailure(ctx, phoneHash, domain.LoginReasonBadCredential, in.TraceID); auditErr != nil {
				return LoginResult{}, auditErr
			}
			return LoginResult{}, smsInvalidCode()
		}
		return LoginResult{}, apperr.Internal("load account failed", err)
	}
	return s.auth.CompleteVerifiedLogin(ctx, VerifiedLoginInput{
		AccountID: account.ID,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		Device:    in.Device,
		TraceID:   in.TraceID,
	})
}

// Register 校验验证码后为未注册手机号开户并直接登录（IDN-P0-02 方案 A）。
//
// 已注册手机号与验证码错误统一返回 40100，避免通过注册端点枚举手机号。
func (s *SMSLoginService) Register(ctx context.Context, in SMSLoginInput) (LoginResult, error) {
	phoneHash, err := s.verifyInput(in)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.consumeCode(ctx, phoneHash, strings.TrimSpace(in.Code), in.TraceID); err != nil {
		return LoginResult{}, err
	}
	return s.auth.RegisterConsumerByPhone(ctx, ConsumerRegisterInput{
		PhoneHash: phoneHash,
		IPHash:    in.IPHash,
		UAHash:    in.UAHash,
		Device:    in.Device,
		TraceID:   in.TraceID,
	})
}

// verifyInput 校验手机号 / 验证码格式并执行登录限流，返回规范化后的手机号摘要。
//
// 限流器不接收 context（进程内 / Redis 实现自带超时），因此本方法不需要 ctx。
func (s *SMSLoginService) verifyInput(in SMSLoginInput) (string, error) {
	phone := strings.TrimSpace(in.Phone)
	code := strings.TrimSpace(in.Code)
	if !e164Pattern.MatchString(phone) || !smsCodePattern.MatchString(code) {
		return "", apperr.BadRequest("phone or code format is invalid")
	}
	phoneHash := PhoneRef(s.hasher, phone)
	accountKey := loginRateKey(domain.ChannelC, phoneHash, "")
	ipKey := ""
	if in.IPHash != "" {
		ipKey = loginRateKey(domain.ChannelC, "", in.IPHash)
	}
	if err := s.guard.CheckRateLimitPair(accountKey, ipKey); err != nil {
		return "", err
	}
	return phoneHash, nil
}

// consumeCode 校验并消费验证码；成功后删除验证码与错误计数，失败走统一 40100。
func (s *SMSLoginService) consumeCode(ctx context.Context, phoneHash, code, traceID string) error {
	storedHash, err := s.codes.Get(ctx, phoneHash)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return smsInvalidCode()
		}
		return unavailableSMS()
	}
	if !SMSCodeMatches(s.hasher, storedHash, phoneHash, code) {
		attempts, incrErr := s.codes.IncrAttempts(ctx, phoneHash, s.codeTTL)
		if incrErr != nil {
			return unavailableSMS()
		}
		if attempts >= s.maxAttempts {
			// 达到上限直接作废验证码，强制重新发送。
			_ = s.codes.Delete(ctx, phoneHash)
			_ = s.codes.ResetAttempts(ctx, phoneHash)
		}
		if err := s.auth.RecordExternalLoginFailure(ctx, phoneHash, domain.LoginReasonBadCredential, traceID); err != nil {
			return err
		}
		return smsInvalidCode()
	}
	_ = s.codes.Delete(ctx, phoneHash)
	_ = s.codes.ResetAttempts(ctx, phoneHash)
	return nil
}

// ConsumerRegisterInput 是 C 端手机号开户入参。
type ConsumerRegisterInput struct {
	// PhoneHash 手机号不可逆摘要；作为账号主体与凭据哈希。
	PhoneHash string
	// IPHash / UAHash 客户端摘要。
	IPHash string
	UAHash string
	// Device 可展示设备画像；由 transport 从原始 UA / IP 派生。
	Device domain.DeviceInfo
	// TraceID 请求关联 ID。
	TraceID string
}

// RegisterConsumerByPhone 在同一事务内创建 C 端账号、手机号凭据与开户审计，并完成登录。
//
// 手机号摘要主体唯一：并发重复开户由唯一键兜底，冲突统一返回 40100。
func (s *AuthService) RegisterConsumerByPhone(ctx context.Context, in ConsumerRegisterInput) (LoginResult, error) {
	if s.channel != domain.ChannelC {
		return LoginResult{}, apperr.BadRequest("consumer registration is only supported on channel=c")
	}
	phoneHash := strings.TrimSpace(in.PhoneHash)
	if phoneHash == "" {
		return LoginResult{}, invalidCredentials()
	}
	var result LoginResult
	if err := s.runTx(ctx, func(txCtx context.Context) error {
		res, err := s.registerConsumerInTx(txCtx, phoneHash, in)
		if err != nil {
			return err
		}
		result = res
		return nil
	}); err != nil {
		return LoginResult{}, err
	}
	s.markActivatedSession(ctx, result.SessionID, result.activatedSession)
	return result, nil
}

// registerConsumerInTx 是 RegisterConsumerByPhone 的事务内实现。
func (s *AuthService) registerConsumerInTx(
	ctx context.Context,
	phoneHash string,
	in ConsumerRegisterInput,
) (LoginResult, error) {
	now := s.clock.Now().UTC()
	accountID, err := s.ids.NextString()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate account id failed", err)
	}
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:      accountID,
		Channel: domain.ChannelC,
		Subject: phoneSubject(phoneHash),
		Now:     now,
	})
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.accounts.Save(ctx, account, 0); err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			// 手机号已注册：与验证码错误统一文案，禁止枚举。
			return LoginResult{}, invalidCredentials()
		}
		return LoginResult{}, apperr.Internal("create account failed", err)
	}
	credentialID, err := s.ids.NextString()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate credential id failed", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        credentialID,
		AccountID: account.ID,
		Type:      domain.CredentialTypePhone,
		Hash:      phoneHash,
		Algorithm: "sha256",
		Now:       now,
	})
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.credentials.Save(ctx, credential); err != nil {
		return LoginResult{}, apperr.Internal("save phone credential failed", err)
	}
	if err := s.emitAudit(ctx, audit.ActionAuthAccountCreated, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeSuccess, domain.LoginReasonRegistration, in.TraceID, nil); err != nil {
		return LoginResult{}, err
	}
	if err := s.guard.Authorize(account, now); err != nil {
		return LoginResult{}, err
	}
	return s.completeLogin(ctx, account, now, in.IPHash, in.UAHash, in.Device, in.TraceID)
}

// RecordExternalLoginFailure 记录外部登录失败审计；actor 使用不可逆账号摘要。
//
// 审计写入失败必须向调用方传播，由调用方失败关闭（5xx），禁止静默丢弃。
func (s *AuthService) RecordExternalLoginFailure(ctx context.Context, accountRef, reason, traceID string) error {
	return s.runTx(ctx, func(txCtx context.Context) error {
		return s.emitAudit(txCtx, audit.ActionAuthLoginFailed, accountRef, "", "account", accountRef,
			audit.OutcomeFailure, reason, traceID, nil)
	})
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
	return NewRateLimitedError(message, retryAfter)
}

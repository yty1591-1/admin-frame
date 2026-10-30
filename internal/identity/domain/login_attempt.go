package domain

import (
	"strings"
	"time"
)

// 登录尝试字段上限，与 migrations/000001 的列宽（VARCHAR(128)）一致。
//
// 生产摘要格式为 `<hash_key_id>:<hex HMAC-SHA256>`（见 app.AccountRef / ClientRef），
// 因此上限必须覆盖「key id ≤16 字节 + 分隔符 + 64 位十六进制」，不能按无前缀的 64 计。
const (
	// maxKeyedDigestLen 带 key id 前缀的摘要长度上限。
	maxKeyedDigestLen = 16 + 1 + 64
	// MaxAccountRefLen 账号不可逆摘要长度上限（含 key id 前缀）。
	MaxAccountRefLen = maxKeyedDigestLen
	// MaxIPHashLen / MaxUAHashLen IP 与 UA 摘要长度上限（含 key id 前缀）。
	MaxIPHashLen = maxKeyedDigestLen
	MaxUAHashLen = maxKeyedDigestLen
	// MaxLoginReasonLen 登录原因最大字节数。
	MaxLoginReasonLen = 64
)

// LoginOutcome 登录结果。
type LoginOutcome string

const (
	// LoginOutcomeSuccess 登录成功。
	LoginOutcomeSuccess LoginOutcome = "success"
	// LoginOutcomeFailure 凭据错误、锁定或停用等失败。
	LoginOutcomeFailure LoginOutcome = "failure"
	// LoginOutcomeDenied 策略拒绝（如缺少 MFA、限流）。
	LoginOutcomeDenied LoginOutcome = "denied"
)

// Validate 校验登录结果取值。
func (o LoginOutcome) Validate() error {
	switch o {
	case LoginOutcomeSuccess, LoginOutcomeFailure, LoginOutcomeDenied:
		return nil
	default:
		return errInvalid(ErrInvalidLoginOutcome, "login outcome %q is not supported", string(o))
	}
}

// String 返回结果字面量。
func (o LoginOutcome) String() string { return string(o) }

// 登录原因常量；进入审计与失败计数，不对外暴露。
const (
	// LoginReasonBadCredential 账号不存在或凭据错误，对外统一文案。
	LoginReasonBadCredential = "bad_credential" //nolint:gosec // G101: 登录失败原因字面量，不是凭证
	// LoginReasonLocked 账号处于锁定期。
	LoginReasonLocked = "locked"
	// LoginReasonDisabled 账号已停用或软删。
	LoginReasonDisabled = "disabled"
	// LoginReasonMFAPending 凭据通过但 MFA 未完成。
	LoginReasonMFAPending = "mfa_pending"
	// LoginReasonMFAFailed MFA 动态码校验失败。
	LoginReasonMFAFailed = "mfa_failed"
	// LoginReasonReplay 刷新令牌重放。
	LoginReasonReplay = "reuse_detected"
	// LoginReasonRegistration C 端手机号自助开户。
	LoginReasonRegistration = "registration"
)

// LoginAttempt 是一次登录尝试的不可变记录。
//
// 隐私约束：AccountRef / IPHash / UAHash 均为不可逆摘要，
// 禁止在此结构或数据库列中出现明文手机号、邮箱、IP 与密码。
type LoginAttempt struct {
	// ID 雪花 ID。
	ID string
	// Channel 端别。
	Channel Channel
	// AccountRef 账号主体摘要（含端别前缀），用于失败计数与审计关联。
	AccountRef string
	// Outcome 登录结果。
	Outcome LoginOutcome
	// Reason 具体原因；成功时可为空。
	Reason string
	// IPHash 客户端 IP 摘要；不可获得时为空。
	IPHash string
	// UAHash User-Agent 摘要；不可获得或为空时为空。
	UAHash string
	// OccurredAt 发生时间（UTC）。
	OccurredAt time.Time
}

// NewLoginAttemptParams 创建登录尝试的入参。
type NewLoginAttemptParams struct {
	ID         string
	Channel    Channel
	AccountRef string
	Outcome    LoginOutcome
	Reason     string
	IPHash     string
	UAHash     string
	OccurredAt time.Time
}

// NewLoginAttempt 校验并创建登录尝试记录。
func NewLoginAttempt(p NewLoginAttemptParams) (*LoginAttempt, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrEmptyInput, "login attempt id is required")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.AccountRef) == "" {
		return nil, errInvalid(ErrInvalidLoginAttempt, "login attempt account ref is required")
	}
	if len(p.AccountRef) > MaxAccountRefLen {
		return nil, errInvalid(ErrInvalidLoginAttempt, "login attempt account ref exceeds %d bytes", MaxAccountRefLen)
	}
	if err := p.Outcome.Validate(); err != nil {
		return nil, err
	}
	if len(p.Reason) > MaxLoginReasonLen {
		return nil, errInvalid(ErrInvalidLoginAttempt, "login attempt reason exceeds %d bytes", MaxLoginReasonLen)
	}
	if len(p.IPHash) > MaxIPHashLen || len(p.UAHash) > MaxUAHashLen {
		return nil, errInvalid(ErrInvalidLoginAttempt, "login attempt ip / ua hash exceeds %d bytes", MaxIPHashLen)
	}
	if p.OccurredAt.IsZero() {
		return nil, errInvalid(ErrInvalidLoginAttempt, "login attempt occurred at is required")
	}
	return &LoginAttempt{
		ID:         p.ID,
		Channel:    p.Channel,
		AccountRef: p.AccountRef,
		Outcome:    p.Outcome,
		Reason:     p.Reason,
		IPHash:     p.IPHash,
		UAHash:     p.UAHash,
		OccurredAt: p.OccurredAt.UTC(),
	}, nil
}

// Package observability 提供身份域核心指标的 Prometheus 适配。
//
// 本包只把应用层观测接口翻译为指标，不参与认证与撤销判定；由 cmd 在装配期创建一次，
// 同时注入 RevocationChecker 与 AuthService，保证读写两侧共享同一 Registry。
package observability

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// Metrics 是身份域指标集，实现 app.Metrics 与 app.RevocationObserver。
type Metrics struct {
	loginTotal           *prometheus.CounterVec
	loginDuration        *prometheus.HistogramVec
	loginTxDuration      *prometheus.HistogramVec
	lockoutTotal         *prometheus.CounterVec
	mfaVerifyTotal       *prometheus.CounterVec
	mfaRecoveryUsed      prometheus.Counter
	refreshTotal         *prometheus.CounterVec
	tokenReuseTotal      prometheus.Counter
	sessionRevokeTotal   *prometheus.CounterVec
	smsSendTotal         *prometheus.CounterVec
	smsProviderAlert     *prometheus.CounterVec
	oauthCallbackTotal   *prometheus.CounterVec
	revocationStoreError *prometheus.CounterVec
	stateWriteErrors     *prometheus.CounterVec
	l1Hits               *prometheus.CounterVec
	redisCalls           *prometheus.CounterVec
	dbRefetches          *prometheus.CounterVec
}

// 编译期断言：指标集必须满足应用层观测端口。
var (
	_ app.Metrics                 = (*Metrics)(nil)
	_ app.RevocationObserver      = (*Metrics)(nil)
	_ app.RevocationCacheObserver = (*Metrics)(nil)
)

// NewMetrics 创建并注册指标；重复注册视为装配错误（fail-fast）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("identity observability: registerer is required")
	}
	m := &Metrics{
		loginTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_login_total",
			Help: "身份域登录尝试次数；result=success|failure|challenge，reason 为粗粒度原因。",
		}, []string{"channel", "result", "reason"}),
		loginDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_login_duration_seconds",
			Help:    "身份域登录耗时（秒），含密码校验与事务写入。",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		loginTxDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_login_tx_duration_seconds",
			Help:    "身份域登录事务阶段耗时（秒），不含事务外密码哈希。",
			Buckets: []float64{0.001, 0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1},
		}, []string{"channel"}),
		lockoutTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_lockout_total",
			Help: "身份域账号锁定次数；仅统计 active/pending → locked 的真实迁移。",
		}, []string{"channel"}),
		mfaVerifyTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_mfa_verify_total",
			Help: "身份域 MFA 校验次数。",
		}, []string{"result"}),
		mfaRecoveryUsed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "identity_mfa_recovery_used_total",
			Help: "身份域恢复码使用次数；任何非零都应通知账号所有者。",
		}),
		refreshTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_refresh_total",
			Help: "身份域刷新令牌轮换次数。",
		}, []string{"result"}),
		tokenReuseTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "identity_token_reuse_total",
			Help: "刷新令牌重放次数；任何非零都应告警。",
		}),
		sessionRevokeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_session_revoke_total",
			Help: "身份域设备会话撤销次数。",
		}, []string{"reason", "result"}),
		smsSendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_sms_send_total",
			Help: "短信验证码发送次数。",
		}, []string{"result"}),
		smsProviderAlert: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_sms_provider_alerts_total",
			Help: "短信供应商告警次数；kind=insufficient_balance 等低基数类型，任何非零都应通知运维。",
		}, []string{"kind", "provider"}),
		oauthCallbackTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_oauth_callback_total",
			Help: "OAuth 授权回调次数。",
		}, []string{"provider", "result"}),
		revocationStoreError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_store_errors_total",
			Help: "撤销存储 / 回源失败次数；op=account_load|session_load|token_load。",
		}, []string{"op"}),
		stateWriteErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_cache_write_error_total",
			Help: "身份域撤销快照写入失败次数；op=set/delete 为账号快照，op=session_set/session_delete 为会话快照（失败时依赖 ≤60s TTL 兜底）。",
		}, []string{"op"}),
		l1Hits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_l1_hit_total",
			Help: "撤销快照本地缓存命中次数；kind=account|session。命中率反映 Redis 往返削减效果。",
		}, []string{"kind"}),
		redisCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_redis_calls_total",
			Help: "撤销检查触发的 Redis 调用次数；op=account_load|session_load|token_load 等。",
		}, []string{"op"}),
		dbRefetches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_db_refetch_total",
			Help: "撤销快照数据库回源次数；kind=account|session。Redis 故障或快照陈旧时增长。",
		}, []string{"kind"}),
	}
	for _, collector := range []prometheus.Collector{
		m.loginTotal, m.loginDuration, m.loginTxDuration, m.lockoutTotal, m.mfaVerifyTotal,
		m.refreshTotal, m.tokenReuseTotal, m.sessionRevokeTotal, m.smsSendTotal, m.smsProviderAlert,
		m.mfaRecoveryUsed, m.oauthCallbackTotal, m.revocationStoreError, m.stateWriteErrors,
		m.l1Hits, m.redisCalls, m.dbRefetches,
	} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("identity observability: register metrics: %w", err)
		}
	}
	m.mfaRecoveryUsed.Add(0)
	m.preinit()
	return m, nil
}

// ObserveMFARecoveryUsed 实现 app.Metrics。
func (m *Metrics) ObserveMFARecoveryUsed(_ context.Context, _ int) {
	if m == nil {
		return
	}
	m.mfaRecoveryUsed.Inc()
}

// preinit 预创建关键标签组合，保证无失败时指标仍可被采集，避免告警表达式因指标缺失而静默。
func (m *Metrics) preinit() {
	for _, channel := range []string{"admin", "biz", "c"} {
		m.loginTotal.WithLabelValues(channel, app.MetricResultSuccess, app.MetricReasonOK)
		m.loginTotal.WithLabelValues(channel, app.MetricResultFailure, app.MetricReasonBadCredential)
		m.loginTxDuration.WithLabelValues(channel)
		m.lockoutTotal.WithLabelValues(channel)
	}
	for _, result := range []string{app.MetricResultSuccess, app.MetricResultFailure} {
		m.mfaVerifyTotal.WithLabelValues(result)
		m.refreshTotal.WithLabelValues(result)
		m.smsSendTotal.WithLabelValues(result)
	}
	for _, op := range []string{"set", "delete", "session_set", "session_delete"} {
		m.stateWriteErrors.WithLabelValues(op)
	}
	for _, op := range []string{"account_load", "session_load", "token_load"} {
		m.revocationStoreError.WithLabelValues(op)
	}
	for _, kind := range []string{"account", "session"} {
		m.l1Hits.WithLabelValues(kind)
		m.dbRefetches.WithLabelValues(kind)
	}
	for _, op := range []string{
		"account_load", "session_load", "token_load", "token_revoke",
		"account_set", "account_delete", "session_set", "session_delete",
	} {
		m.redisCalls.WithLabelValues(op)
	}
	for _, reason := range []string{
		string(domain.RevokeReasonLogout), string(domain.RevokeReasonLogoutAll),
		string(domain.RevokeReasonReplay), string(domain.RevokeReasonAdmin),
		string(domain.RevokeReasonAccountDisabled), string(domain.RevokeReasonPasswordChanged),
		string(domain.RevokeReasonPhoneChanged), string(domain.RevokeReasonEvicted),
		string(domain.RevokeReasonExpired),
	} {
		m.sessionRevokeTotal.WithLabelValues(reason, app.MetricResultSuccess)
		m.sessionRevokeTotal.WithLabelValues(reason, app.MetricResultFailure)
	}
}

// ObserveLogin 实现 app.Metrics。
func (m *Metrics) ObserveLogin(_ context.Context, channel domain.Channel, result, reason string, duration time.Duration) {
	if m == nil {
		return
	}
	m.loginTotal.WithLabelValues(channel.String(), result, reason).Inc()
	m.loginDuration.WithLabelValues(channel.String()).Observe(duration.Seconds())
}

// ObserveLoginTxDuration 实现 app.Metrics。
func (m *Metrics) ObserveLoginTxDuration(_ context.Context, channel domain.Channel, duration time.Duration) {
	if m == nil {
		return
	}
	m.loginTxDuration.WithLabelValues(channel.String()).Observe(duration.Seconds())
}

// ObserveLockout 实现 app.Metrics。
func (m *Metrics) ObserveLockout(_ context.Context, channel domain.Channel) {
	if m == nil {
		return
	}
	m.lockoutTotal.WithLabelValues(channel.String()).Inc()
}

// ObserveMFAVerify 实现 app.Metrics。
func (m *Metrics) ObserveMFAVerify(_ context.Context, result string) {
	if m == nil {
		return
	}
	m.mfaVerifyTotal.WithLabelValues(result).Inc()
}

// ObserveRefresh 实现 app.Metrics。
func (m *Metrics) ObserveRefresh(_ context.Context, result string) {
	if m == nil {
		return
	}
	m.refreshTotal.WithLabelValues(result).Inc()
}

// ObserveTokenReuse 实现 app.Metrics。
func (m *Metrics) ObserveTokenReuse(_ context.Context) {
	if m == nil {
		return
	}
	m.tokenReuseTotal.Inc()
}

// ObserveSessionRevoke 实现 app.Metrics。
func (m *Metrics) ObserveSessionRevoke(_ context.Context, reason, result string) {
	if m == nil {
		return
	}
	m.sessionRevokeTotal.WithLabelValues(reason, result).Inc()
}

// ObserveSMSProviderAlert 记录一次供应商告警（kind / provider 均为低基数标签）。
func (m *Metrics) ObserveSMSProviderAlert(kind, provider string) {
	if m == nil {
		return
	}
	m.smsProviderAlert.WithLabelValues(kind, provider).Inc()
}

// ObserveSMSSend 实现 app.Metrics。
func (m *Metrics) ObserveSMSSend(_ context.Context, result string) {
	if m == nil {
		return
	}
	m.smsSendTotal.WithLabelValues(result).Inc()
}

// ObserveOAuthCallback 实现 app.Metrics。
func (m *Metrics) ObserveOAuthCallback(_ context.Context, provider, result string) {
	if m == nil {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	m.oauthCallbackTotal.WithLabelValues(provider, result).Inc()
}

// OnAccountStateWriteFailure 实现 app.RevocationObserver。
func (m *Metrics) OnAccountStateWriteFailure(_ context.Context, op string, _ error) {
	if m == nil {
		return
	}
	if op == "" {
		op = "unknown"
	}
	m.stateWriteErrors.WithLabelValues(op).Inc()
}

// OnRevocationStoreFailure 记录撤销存储读失败 / 回源失败。
func (m *Metrics) OnRevocationStoreFailure(_ context.Context, op string, _ error) {
	if m == nil {
		return
	}
	if op == "" {
		op = "unknown"
	}
	m.revocationStoreError.WithLabelValues(op).Inc()
}

// OnRevocationCacheHit 实现 app.RevocationCacheObserver。
func (m *Metrics) OnRevocationCacheHit(_ context.Context, kind string) {
	if m == nil {
		return
	}
	if kind == "" {
		kind = "unknown"
	}
	m.l1Hits.WithLabelValues(kind).Inc()
}

// OnRevocationRedisCall 实现 app.RevocationCacheObserver。
func (m *Metrics) OnRevocationRedisCall(_ context.Context, op string) {
	if m == nil {
		return
	}
	if op == "" {
		op = "unknown"
	}
	m.redisCalls.WithLabelValues(op).Inc()
}

// OnRevocationDBRefetch 实现 app.RevocationCacheObserver。
func (m *Metrics) OnRevocationDBRefetch(_ context.Context, kind string) {
	if m == nil {
		return
	}
	if kind == "" {
		kind = "unknown"
	}
	m.dbRefetches.WithLabelValues(kind).Inc()
}

// Package observability 提供身份域核心指标的 Prometheus 适配。
//
// 本包只把应用层观测接口翻译为指标，不参与认证与撤销判定；由 cmd 在装配期创建一次，
// 同时注入 RevocationChecker 与 AuthService，保证读写两侧共享同一 Registry。
package observability

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// Metrics 是身份域指标集，实现 app.Metrics 与 app.RevocationObserver。
type Metrics struct {
	loginTotal            *prometheus.CounterVec
	loginDuration         *prometheus.HistogramVec
	loginTxDuration       *prometheus.HistogramVec
	lockoutTotal          *prometheus.CounterVec
	mfaVerifyTotal        *prometheus.CounterVec
	mfaRecoveryUsed       prometheus.Counter
	refreshTotal          *prometheus.CounterVec
	refreshDuration       *prometheus.HistogramVec
	mfaVerifyDuration     *prometheus.HistogramVec
	stepUpDuration        *prometheus.HistogramVec
	oauthCallbackDuration *prometheus.HistogramVec
	tokenReuseTotal       prometheus.Counter
	sessionRevokeTotal    *prometheus.CounterVec
	smsSendTotal          *prometheus.CounterVec
	smsProviderAlert      *prometheus.CounterVec
	oauthCallbackTotal    *prometheus.CounterVec
	revocationStoreError  *prometheus.CounterVec
	stateWriteErrors      *prometheus.CounterVec
	l1Hits                *prometheus.CounterVec
	redisCalls            *prometheus.CounterVec
	dbRefetches           *prometheus.CounterVec
	registerer            prometheus.Registerer
	// loginCount / startedAt 用于 identity_login_qps（PERF-08 的 HPA 辅助信号）。
	loginCount atomic.Uint64
	startedAt  time.Time
}

// 编译期断言：指标集必须满足应用层观测端口。
var (
	_ app.Metrics                 = (*Metrics)(nil)
	_ app.RevocationObserver      = (*Metrics)(nil)
	_ app.RevocationCacheObserver = (*Metrics)(nil)
)

// CleanupMetrics 是清理任务的独立指标集，供 worker 进程单独注册（OPS-02）。
type CleanupMetrics struct {
	runs           *prometheus.CounterVec
	rows           *prometheus.CounterVec
	duration       prometheus.Histogram
	batchLimitHits prometheus.Counter
}

// 编译期断言。
var _ app.CleanupObserver = (*CleanupMetrics)(nil)

// NewCleanupMetrics 创建并注册清理指标；重复注册视为装配错误（fail-fast）。
func NewCleanupMetrics(registerer prometheus.Registerer) (*CleanupMetrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("identity cleanup observability: registerer is required")
	}
	m := &CleanupMetrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_cleanup_runs_total",
			Help: "身份域清理任务执行轮次；result=success|failure|skipped。",
		}, []string{"result"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_cleanup_rows_total",
			Help: "身份域清理任务处理的累计行数；kind 为 expired/deleted/archived 等低基数类型。",
		}, []string{"kind"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "identity_cleanup_duration_seconds",
			Help:    "身份域单轮清理耗时（秒）；包含过期标记、归档、删除与登录尝试清理。",
			Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600, 1800, 3600},
		}),
		batchLimitHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "identity_cleanup_batch_limit_hit_total",
			Help: "身份域清理达到单轮批次上限仍未清空的次数；非零表示积压或吞吐不足。",
		}),
	}
	for _, collector := range []prometheus.Collector{m.runs, m.rows, m.duration, m.batchLimitHits} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("identity cleanup observability: register metrics: %w", err)
		}
	}
	for _, result := range []string{"success", "failure", "skipped"} {
		m.runs.WithLabelValues(result)
	}
	for _, kind := range []string{
		"expired_sessions", "expired_families",
		"deleted_sessions", "deleted_families",
		"archived_sessions", "archived_families",
		"deleted_archived_sessions", "deleted_archived_families",
		"deleted_login_attempts",
	} {
		m.rows.WithLabelValues(kind)
	}
	m.batchLimitHits.Add(0)
	return m, nil
}

// OnCleanupRun 实现 app.CleanupObserver。
func (m *CleanupMetrics) OnCleanupRun(_ context.Context, result app.CleanupRunResult) {
	if m == nil {
		return
	}
	status := "success"
	switch {
	case result.Skipped:
		status = "skipped"
	case result.Err != nil:
		status = "failure"
	}
	m.runs.WithLabelValues(status).Inc()
	if result.Skipped {
		return
	}
	m.duration.Observe(result.Duration.Seconds())
	for kind, value := range map[string]int{
		"expired_sessions":          result.Stats.ExpiredSessions,
		"expired_families":          result.Stats.ExpiredFamilies,
		"deleted_sessions":          result.Stats.DeletedSessions,
		"deleted_families":          result.Stats.DeletedFamilies,
		"archived_sessions":         result.Stats.ArchivedSessions,
		"archived_families":         result.Stats.ArchivedFamilies,
		"deleted_archived_sessions": result.Stats.DeletedArchiveSessions,
		"deleted_archived_families": result.Stats.DeletedArchiveFamilies,
		"deleted_login_attempts":    result.Stats.DeletedLoginAttempts,
	} {
		if value > 0 {
			m.rows.WithLabelValues(kind).Add(float64(value))
		}
	}
	if result.Stats.BatchLimitHit {
		m.batchLimitHits.Inc()
	}
}

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
		refreshDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_refresh_duration_seconds",
			Help:    "身份域刷新令牌耗时（秒），含轮换写入与撤销快照回写。",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		}, []string{"result"}),
		mfaVerifyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_mfa_verify_duration_seconds",
			Help:    "身份域 MFA 校验耗时（秒），含 TOTP / 恢复码校验与票据消费。",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		}, []string{"result"}),
		stepUpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_step_up_duration_seconds",
			Help:    "身份域管理员二次 MFA（step-up）校验耗时（秒）。",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		}, []string{"result"}),
		oauthCallbackDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "identity_oauth_callback_duration_seconds",
			Help:    "身份域 OAuth 回调耗时（秒），含 state 消费与 IdP 换取。",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10},
		}, []string{"provider", "result"}),
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
		m.refreshTotal, m.refreshDuration, m.mfaVerifyDuration, m.stepUpDuration,
		m.oauthCallbackDuration, m.tokenReuseTotal, m.sessionRevokeTotal, m.smsSendTotal,
		m.smsProviderAlert, m.mfaRecoveryUsed, m.oauthCallbackTotal, m.revocationStoreError,
		m.stateWriteErrors, m.l1Hits, m.redisCalls, m.dbRefetches,
	} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("identity observability: register metrics: %w", err)
		}
	}
	m.mfaRecoveryUsed.Add(0)
	m.preinit()
	m.registerer = registerer
	m.startedAt = time.Now()
	qps := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "identity_login_qps",
		Help: "身份域自进程启动以来的平均登录 QPS（含成功 / 失败 / 挑战）；用于 HPA 辅助信号。",
	}, func() float64 {
		elapsed := time.Since(m.startedAt).Seconds()
		if elapsed < 1 {
			return 0
		}
		return float64(m.loginCount.Load()) / elapsed
	})
	if err := registerer.Register(qps); err != nil {
		return nil, fmt.Errorf("identity observability: register login qps gauge: %w", err)
	}
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
		m.refreshDuration.WithLabelValues(result)
		m.mfaVerifyDuration.WithLabelValues(result)
		m.stepUpDuration.WithLabelValues(result)
		m.smsSendTotal.WithLabelValues(result)
		m.oauthCallbackDuration.WithLabelValues("unknown", result)
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
	m.loginCount.Add(1)
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

// ObserveRefreshDuration 实现 app.Metrics。
func (m *Metrics) ObserveRefreshDuration(_ context.Context, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.refreshDuration.WithLabelValues(result).Observe(duration.Seconds())
}

// ObserveMFAVerifyDuration 实现 app.Metrics。
func (m *Metrics) ObserveMFAVerifyDuration(_ context.Context, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.mfaVerifyDuration.WithLabelValues(result).Observe(duration.Seconds())
}

// ObserveStepUpDuration 实现 app.Metrics。
func (m *Metrics) ObserveStepUpDuration(_ context.Context, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.stepUpDuration.WithLabelValues(result).Observe(duration.Seconds())
}

// ObserveOAuthCallbackDuration 实现 app.Metrics。
func (m *Metrics) ObserveOAuthCallbackDuration(_ context.Context, provider, result string, duration time.Duration) {
	if m == nil {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	m.oauthCallbackDuration.WithLabelValues(provider, result).Observe(duration.Seconds())
}

// RegisterSessionGauge 注册 identity_active_sessions，供 HPA / Dashboard 观察在线设备（PERF-08）。
//
// GaugeFunc 在抓取时查询一次，查询失败按 0 暴露；调用方需保证查询带超时。
func (m *Metrics) RegisterSessionGauge(stats func() (int64, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("identity observability: metrics is not initialized")
	}
	if stats == nil {
		return fmt.Errorf("identity observability: session stats function is required")
	}
	gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "identity_active_sessions",
		Help: "身份域当前活跃设备会话数；用于容量规划与 HPA 辅助信号。",
	}, func() float64 {
		count, err := stats()
		if err != nil || count < 0 {
			return 0
		}
		return float64(count)
	})
	if err := m.registerer.Register(gauge); err != nil {
		return fmt.Errorf("identity observability: register session gauge: %w", err)
	}
	return nil
}

// RegisterSMSBreakerGauge 注册 identity_sms_breaker_open（OPS-07）。
//
// 取值 1 表示熔断打开或半开（供应商不可用），0 表示闭合；状态变化仍会写告警日志。
func (m *Metrics) RegisterSMSBreakerGauge(state func() string) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("identity observability: metrics is not initialized")
	}
	if state == nil {
		return fmt.Errorf("identity observability: sms breaker state function is required")
	}
	gauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "identity_sms_breaker_open",
		Help: "短信供应商熔断状态；1 表示 open / half-open，0 表示 closed。",
	}, func() float64 {
		if state() == "closed" {
			return 0
		}
		return 1
	})
	if err := m.registerer.Register(gauge); err != nil {
		return fmt.Errorf("identity observability: register sms breaker gauge: %w", err)
	}
	return nil
}

// RegisterOutboxGauges 注册 identity_outbox_pending 与 identity_outbox_lag_seconds（OPS-04）。
//
// 与 IAM 同口径：查询失败按「不可观测」暴露 0，投递失败由积压年龄与日志暴露。
func (m *Metrics) RegisterOutboxGauges(stats func() (int, float64, error)) error {
	if m == nil || m.registerer == nil {
		return fmt.Errorf("identity observability: metrics is not initialized")
	}
	if stats == nil {
		return fmt.Errorf("identity observability: outbox stats function is required")
	}
	pending := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "identity_outbox_pending",
		Help: "身份域 Outbox 未投递事件数（含重试等待与死信）；持续 >0 说明 identity.audit 投递受阻。",
	}, func() float64 {
		count, _, err := stats()
		if err != nil || count < 0 {
			return 0
		}
		return float64(count)
	})
	lag := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "identity_outbox_lag_seconds",
		Help: "身份域 Outbox 最老未投递事件的积压秒数；持续增长触发 P2 告警。",
	}, func() float64 {
		_, seconds, err := stats()
		if err != nil || seconds < 0 {
			return 0
		}
		return seconds
	})
	for name, collector := range map[string]prometheus.Collector{
		"pending": pending,
		"lag":     lag,
	} {
		if err := m.registerer.Register(collector); err != nil {
			return fmt.Errorf("identity observability: register outbox %s gauge: %w", name, err)
		}
	}
	return nil
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

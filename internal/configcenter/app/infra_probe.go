package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/redis"
	storagepkg "github.com/admin-frame/admin-frame/pkg/storage"
)

const (
	probePerTargetTimeout = 3 * time.Second
	probeTotalBudget      = 15 * time.Second
	probeDetailMaxLen     = 160
)

// ProbeResult 是单个基础设施探测失败项。
type ProbeResult struct {
	Ref    string // namespace/key
	Reason string // connectivity_failed | deploy_env_missing | secret_missing
	Detail string // 截断、无密钥
}

// ProbeInput 是连通性探测入参。
type ProbeInput struct {
	Env    string
	Values map[string]string
	// Secrets 可选；提供时解析 redis/nats/smtp/storage 敏感值。
	Secrets *SecretService
	// DB 可选；已装配的数据库句柄，用于 mysql Ping。
	DB *db.DB
	// ReportDeployEnvGaps 为 true 时，mysql 缺 ADMIN_FRAME_DB_DSN 记入 gaps
	// （Complete / config-api 启动）；日常 Status 轮询为 false，避免安装中途刷屏。
	ReportDeployEnvGaps bool
}

// RunInfraProbes 按条件探测 DB/Redis/Storage/NATS/SMTP；跳过未启用的目标。
func RunInfraProbes(ctx context.Context, in ProbeInput) []ProbeResult {
	if in.Values == nil {
		in.Values = map[string]string{}
	}
	deadline := time.Now().Add(probeTotalBudget)
	out := make([]ProbeResult, 0, 4)
	out = append(out, probeDB(ctx, deadline, in)...)
	out = append(out, probeRedis(ctx, deadline, in)...)
	out = append(out, probeStorage(ctx, deadline, in)...)
	out = append(out, probeNATS(ctx, deadline, in)...)
	out = append(out, probeSMTP(ctx, deadline, in)...)
	return out
}

// ProbeResultsToGaps 把探测失败转成 RequiredGap（可编辑键进入 repair 白名单）。
func ProbeResultsToGaps(results []ProbeResult) []RequiredGap {
	out := make([]RequiredGap, 0, len(results))
	for _, r := range results {
		namespace, key, ok := strings.Cut(r.Ref, "/")
		if !ok {
			continue
		}
		// DB DSN 不进向导白名单：仍透出 gap 供 status 展示。
		out = append(out, RequiredGap{Namespace: namespace, Key: key, Reason: r.Reason})
	}
	return out
}

// RepairableProbeRefs 返回可写入 repair_keys 的探测失败引用（排除 db.dsn）。
func RepairableProbeRefs(results []ProbeResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.Ref == spec.NamespacePlatform+"/db.dsn" || r.Reason == spec.GapReasonDeployEnvMissing {
			continue
		}
		out = append(out, r.Ref)
	}
	return out
}

func probeDB(ctx context.Context, deadline time.Time, in ProbeInput) []ProbeResult {
	driver := strings.ToLower(strings.TrimSpace(in.Values["db.driver"]))
	if driver != "mysql" && driver != "mariadb" {
		return nil
	}
	dsn := strings.TrimSpace(os.Getenv("ADMIN_FRAME_DB_DSN"))
	if dsn == "" {
		// DSN 不进向导：缺失时仅在 ReportDeployEnvGaps 为真（Complete/启动）时报告。
		if !in.ReportDeployEnvGaps {
			return nil
		}
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/db.dsn",
			Reason: spec.GapReasonDeployEnvMissing,
			Detail: "ADMIN_FRAME_DB_DSN is required when db.driver=mysql",
		}}
	}
	pctx, cancel := probeContext(ctx, deadline)
	defer cancel()
	if in.DB != nil {
		if err := in.DB.Ping(pctx); err != nil {
			return []ProbeResult{{
				Ref:    spec.NamespacePlatform + "/db.driver",
				Reason: spec.GapReasonConnectivityFailed,
				Detail: sanitizeProbeErr(err),
			}}
		}
		return nil
	}
	opened, err := db.Open(pctx, db.Config{Driver: driver, DSN: dsn})
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/db.driver",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	defer func() { _ = opened.Close() }()
	if err := opened.Ping(pctx); err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/db.driver",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	return nil
}

func probeRedis(ctx context.Context, deadline time.Time, in ProbeInput) []ProbeResult {
	addr := strings.TrimSpace(in.Values["redis.addr"])
	if addr == "" {
		if !spec.RedisRequired(in.Values) {
			return nil
		}
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/redis.addr",
			Reason: spec.GapReasonMissingInSnapshot,
			Detail: "redis.addr is required when redis-backed features are enabled",
		}}
	}
	password := peekOptional(ctx, in, spec.NamespacePlatform, "redis.password")
	dbIndex, _ := strconv.Atoi(strings.TrimSpace(in.Values["redis.db"]))
	pctx, cancel := probeContext(ctx, deadline)
	defer cancel()
	client, err := redis.Open(pctx, redis.Config{
		Addr:     addr,
		Username: strings.TrimSpace(in.Values["redis.username"]),
		Password: password,
		DB:       dbIndex,
	})
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/redis.addr",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	defer func() { _ = client.Close() }()
	return nil
}

func probeStorage(ctx context.Context, deadline time.Time, in ProbeInput) []ProbeResult {
	driver := strings.ToLower(strings.TrimSpace(in.Values["storage.driver"]))
	if driver == "" || driver == "memory" {
		return nil
	}
	cfg := platformstorage.Config{
		Driver:    driver,
		KeyPrefix: strings.TrimSpace(in.Values["storage.key_prefix"]),
	}
	// storage.local.* 按 STANDARDS §3.1.1 属节点部署，不进 KeySpec；探测时从
	// 快照旁路值 / 部署 env / 本机演练默认根目录解析。
	cfg.Local.Root = resolveLocalStorageRoot(in.Values)
	cfg.S3.Bucket = strings.TrimSpace(in.Values["storage.s3.bucket"])
	cfg.S3.Region = strings.TrimSpace(in.Values["storage.s3.region"])
	cfg.S3.Endpoint = strings.TrimSpace(in.Values["storage.s3.endpoint"])
	cfg.S3.ForcePathStyle = strings.EqualFold(strings.TrimSpace(in.Values["storage.s3.force_path_style"]), "true")
	cfg.S3.AccessKey = peekOptional(ctx, in, spec.NamespacePlatform, "storage.s3.access_key")
	cfg.S3.SecretKey = peekOptional(ctx, in, spec.NamespacePlatform, "storage.s3.secret_key")

	pctx, cancel := probeContext(ctx, deadline)
	defer cancel()
	store, err := platformstorage.OpenStorage(pctx, cfg)
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/storage.driver",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	readyRef := spec.NamespacePlatform + "/storage.driver"
	if driver == "s3" || driver == "minio" {
		readyRef = spec.NamespacePlatform + "/storage.s3.bucket"
	}
	if err := storagepkg.Ready(pctx, store); err != nil {
		return []ProbeResult{{
			Ref:    readyRef,
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	return nil
}

// resolveLocalStorageRoot 解析本地盘根目录（部署侧，不进配置中心 Item）。
func resolveLocalStorageRoot(values map[string]string) string {
	if root := strings.TrimSpace(values["storage.local.root"]); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("ADMIN_FRAME_STORAGE_LOCAL_ROOT")); root != "" {
		return root
	}
	return "var/prod-storage"
}

func probeNATS(ctx context.Context, deadline time.Time, in ProbeInput) []ProbeResult {
	enabled := strings.EqualFold(strings.TrimSpace(in.Values["async.enabled"]), "true")
	driver := strings.ToLower(strings.TrimSpace(in.Values["async.driver"]))
	if !enabled || driver != "nats" {
		return nil
	}
	url := peekOptional(ctx, in, spec.NamespacePlatform, "async.broker.url")
	if url == "" {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/async.broker.url",
			Reason: spec.GapReasonSecretMissing,
			Detail: "async.broker.url secret is required when async.driver=nats",
		}}
	}
	pctx, cancel := probeContext(ctx, deadline)
	defer cancel()
	nc, err := nats.Connect(url,
		nats.Name("config-api-infra-probe"),
		nats.Timeout(probePerTargetTimeout),
		nats.MaxReconnects(0),
	)
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/async.broker.url",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	defer nc.Close()
	if err := nc.FlushWithContext(pctx); err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/async.broker.url",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	js, err := nc.JetStream()
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespacePlatform + "/async.broker.url",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	stream := strings.TrimSpace(in.Values["async.broker.stream"])
	if stream == "" {
		stream = "async"
	}
	if _, err := js.StreamInfo(stream); err != nil {
		// 流不存在仍算「可达」——安装期可能尚未建流；仅网络/鉴权失败才阻塞。
		if !errors.Is(err, nats.ErrStreamNotFound) {
			return []ProbeResult{{
				Ref:    spec.NamespacePlatform + "/async.broker.url",
				Reason: spec.GapReasonConnectivityFailed,
				Detail: sanitizeProbeErr(err),
			}}
		}
	}
	return nil
}

func probeSMTP(ctx context.Context, deadline time.Time, in ProbeInput) []ProbeResult {
	if !strings.EqualFold(strings.TrimSpace(in.Values["notification.channels.email.driver"]), "smtp") {
		return nil
	}
	host := strings.TrimSpace(in.Values["notification.channels.email.smtp.host"])
	if host == "" {
		return nil // 缺键由 MissingInfraRefs 处理
	}
	port := 587
	if raw := strings.TrimSpace(in.Values["notification.channels.email.smtp.port"]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			port = parsed
		}
	}
	password := peekOptional(ctx, in, spec.NamespaceNotification, "notification.smtp.password")
	username := strings.TrimSpace(in.Values["notification.channels.email.smtp.username"])
	encryption := strings.ToLower(strings.TrimSpace(in.Values["notification.channels.email.smtp.encryption"]))
	if encryption == "" {
		encryption = "starttls"
	}
	authMode := strings.ToLower(strings.TrimSpace(in.Values["notification.channels.email.smtp.auth"]))
	if authMode == "" {
		authMode = "plain"
	}
	allowInsecure := strings.EqualFold(strings.TrimSpace(
		in.Values["notification.channels.email.smtp.allow_insecure"]), "true")

	if authMode != "none" && password == "" {
		return []ProbeResult{{
			Ref:    spec.NamespaceNotification + "/notification.smtp.password",
			Reason: spec.GapReasonSecretMissing,
			Detail: "notification.smtp.password secret is required when email.driver=smtp",
		}}
	}

	pctx, cancel := probeContext(ctx, deadline)
	defer cancel()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: probePerTargetTimeout}
	var conn net.Conn
	var err error
	if encryption == "tls" {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: host,
			InsecureSkipVerify: allowInsecure, //nolint:gosec // 探测遵循 allow_insecure
		}}
		conn, err = tlsDialer.DialContext(pctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(pctx, "tcp", addr)
	}
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespaceNotification + "/notification.channels.email.smtp.host",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(probePerTargetTimeout))

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return []ProbeResult{{
			Ref:    spec.NamespaceNotification + "/notification.channels.email.smtp.host",
			Reason: spec.GapReasonConnectivityFailed,
			Detail: sanitizeProbeErr(err),
		}}
	}
	defer func() { _ = client.Close() }()

	if encryption == "starttls" {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: host,
			InsecureSkipVerify: allowInsecure, //nolint:gosec // 探测遵循 allow_insecure
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return []ProbeResult{{
				Ref:    spec.NamespaceNotification + "/notification.channels.email.smtp.host",
				Reason: spec.GapReasonConnectivityFailed,
				Detail: sanitizeProbeErr(err),
			}}
		}
	}
	if authMode != "none" {
		var auth smtp.Auth
		if authMode == "login" {
			auth = &smtpLoginAuth{username: username, password: password}
		} else {
			auth = smtp.PlainAuth("", username, password, host)
		}
		if err := client.Auth(auth); err != nil {
			return []ProbeResult{{
				Ref:    spec.NamespaceNotification + "/notification.smtp.password",
				Reason: spec.GapReasonConnectivityFailed,
				Detail: sanitizeProbeErr(err),
			}}
		}
	}
	_ = client.Quit()
	return nil
}

type smtpLoginAuth struct {
	username, password string
}

// Start 实现 smtp.Auth，声明 LOGIN 机制。
func (a *smtpLoginAuth) Start(_ *smtp.ServerInfo) (mech string, resp []byte, err error) {
	return "LOGIN", nil, nil
}

// Next 实现 smtp.Auth，按服务端提示回传用户名或口令。
func (a *smtpLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(prompt, "username"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected smtp login prompt")
	}
}

func peekOptional(ctx context.Context, in ProbeInput, namespace, name string) string {
	if in.Secrets == nil {
		return ""
	}
	plain, err := in.Secrets.PeekActivePlaintext(ctx, in.Env, namespace, name)
	if err != nil {
		return ""
	}
	return plain
}

func probeContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	timeout := probePerTargetTimeout
	if remaining < timeout {
		timeout = remaining
	}
	return context.WithTimeout(parent, timeout)
}

func sanitizeProbeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// 去掉可能夹带的 user:pass@。
	if at := strings.Index(msg, "@"); at > 0 {
		if scheme := strings.Index(msg, "://"); scheme >= 0 && scheme < at {
			msg = msg[:scheme+3] + "***" + msg[at:]
		}
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > probeDetailMaxLen {
		return msg[:probeDetailMaxLen] + "…"
	}
	return msg
}

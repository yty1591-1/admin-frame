package secrets

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/httpclient"
)

// Vault KV 读取的默认值。
const (
	// defaultVaultMount KV 引擎挂载点。
	defaultVaultMount = "secret"
	// defaultVaultTokenEnv 默认读取令牌的环境变量名（Vault 官方约定）。
	defaultVaultTokenEnv = "VAULT_TOKEN"
	// defaultVaultValueField 逐键模式下承载取值的字段名。
	defaultVaultValueField = "value"
	// defaultVaultTimeout 单次请求超时；启动期总预算由调用方 context 控制。
	defaultVaultTimeout = 5 * time.Second
	// defaultVaultCacheTTL 取值缓存时长；启动期批量读取只应产生一次远端请求。
	defaultVaultCacheTTL = 60 * time.Second
	// maxVaultResponseBytes 响应体上限，防止异常响应拖垮启动。
	maxVaultResponseBytes = 1 << 20
	// maxVaultErrorSnippet 错误响应片段上限（不含密钥值，仅 Vault 错误描述）。
	maxVaultErrorSnippet = 200
)

// errVaultNotFound 表示 Vault 返回 404（路径或字段不存在）。
//
// 与公开的 ErrNotFound 分开：逐键模式可把它翻译成「该密钥不存在」，
// 单对象模式下则视为部署配置错误 —— 配置的对象整体缺失必须阻断启动，
// 否则会把「路径写错」静默降级成「密钥都不存在」。
var errVaultNotFound = errors.New("vault: path not found")

// VaultConfig 是 Vault KV 适配的构造参数。
//
// 两种读数布局（二选一，不允许同时配置）：
//   - 单对象模式（Path）：一个 KV 对象承载全部密钥，字段名即逻辑密钥名
//     （`db_dsn`、`auth_jwt_secret`…）。启动期只发一次请求，推荐。
//   - 逐键模式（Prefix）：每个密钥一个对象，路径为 `<prefix>/<name>`，
//     取值从 `ValueField` 字段读取（默认 `value`）。
type VaultConfig struct {
	// Address Vault 地址，如 https://vault.internal:8200；必填。
	Address string
	// Token 显式令牌；仅用于本地调试，生产应使用 TokenFile / TokenEnv。
	Token string
	// TokenFile 存放令牌的文件（K8s ServiceAccount / Vault Agent 投影）。
	TokenFile string
	// TokenEnv 令牌所在环境变量名；默认 VAULT_TOKEN。
	TokenEnv string
	// Mount KV 引擎挂载点；默认 secret。
	Mount string
	// Path 单对象模式的对象路径（不含 mount 与 data 段），如 admin-frame/prod。
	Path string
	// Prefix 逐键模式的对象前缀，如 admin-frame/prod。
	Prefix string
	// ValueField 逐键模式下的取值字段名；默认 value。
	ValueField string
	// Namespace Vault Enterprise 命名空间；空表示不设置。
	Namespace string
	// CAFile 自定义 CA 证书（自签 Vault）；空表示用系统根证书。
	CAFile string
	// Timeout 单次请求超时；<=0 时取 5s。
	Timeout time.Duration
	// CacheTTL 取值缓存时长；0 取 60s，负数表示不缓存。
	CacheTTL time.Duration
	// Logger 访问日志；nil 使用 slog.Default。只记录路径与字段数，绝不记录取值。
	Logger *slog.Logger
}

// VaultProvider 通过 Vault HTTP API 读取 KV 密钥（ENT-07 T3）。
//
// 实现约束：
//   - 只依赖标准库与 pkg/httpclient，不引入 Vault SDK（避免新增直接依赖与
//     DEP-06 评审面）；KV v1 / v2 由首次读取自动探测并记忆。
//   - 网络错误、403、5xx、响应解析失败都返回**非** ErrNotFound 的错误，
//     使 Chain 立即中止而不是回落到环境变量（生产禁止静默回落）。
//   - 令牌只出现在请求头；错误与日志只包含路径、状态码，不含令牌与取值。
type VaultProvider struct {
	baseURL    *url.URL
	token      string
	mount      string
	path       string
	prefix     string
	valueField string
	namespace  string
	cacheTTL   time.Duration
	client     *httpclient.Client
	logger     *slog.Logger

	mu        sync.Mutex
	closed    bool
	kvVersion int // 0 表示尚未探测（2 = KV v2，1 = KV v1）
	cache     map[string]vaultCacheEntry

	// 单对象模式的对象级缓存：字段缺失也必须在 TTL 内不重复拉取，
	// 因此不能只按字段名缓存命中的取值。
	objectFields  map[string]string
	objectExpires time.Time
}

// vaultCacheEntry 是单个逻辑密钥的缓存项。
type vaultCacheEntry struct {
	value   string
	expires time.Time
}

// NewVaultProvider 构造 Vault Provider；参数非法或令牌缺失时返回错误（启动期即失败）。
func NewVaultProvider(cfg VaultConfig) (*VaultProvider, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, errors.New("secrets: vault address is required")
	}
	baseURL, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("secrets: parse vault address: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("secrets: vault address must use http or https, got %q", baseURL.Scheme)
	}
	if baseURL.Host == "" {
		return nil, errors.New("secrets: vault address must include host")
	}

	objectPath := strings.Trim(strings.TrimSpace(cfg.Path), "/")
	prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if objectPath != "" && prefix != "" {
		return nil, errors.New("secrets: vault path and prefix are mutually exclusive")
	}
	if objectPath == "" && prefix == "" {
		return nil, errors.New("secrets: vault requires path (single object) or prefix (per-key)")
	}
	for _, candidate := range []string{objectPath, prefix} {
		if candidate == "" {
			continue
		}
		if err := validateVaultRelPath(candidate); err != nil {
			return nil, err
		}
	}

	mount := strings.Trim(strings.TrimSpace(cfg.Mount), "/")
	if mount == "" {
		mount = defaultVaultMount
	}
	if err := validateVaultRelPath(mount); err != nil {
		return nil, fmt.Errorf("secrets: invalid vault mount: %w", err)
	}

	token, err := resolveVaultToken(cfg)
	if err != nil {
		return nil, err
	}

	valueField := strings.TrimSpace(cfg.ValueField)
	if valueField == "" {
		valueField = defaultVaultValueField
	}
	if err := validateVaultRelPath(valueField); err != nil {
		return nil, fmt.Errorf("secrets: invalid vault value field: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultVaultTimeout
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = defaultVaultCacheTTL
	}

	client, err := newVaultHTTPClient(timeout, strings.TrimSpace(cfg.CAFile))
	if err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &VaultProvider{
		baseURL:    baseURL,
		token:      token,
		mount:      mount,
		path:       objectPath,
		prefix:     prefix,
		valueField: valueField,
		namespace:  strings.TrimSpace(cfg.Namespace),
		cacheTTL:   cacheTTL,
		client:     client,
		logger:     logger,
		cache:      make(map[string]vaultCacheEntry),
	}, nil
}

// Get 读取逻辑密钥；字段缺失返回 ErrNotFound，传输/鉴权/解析失败返回硬错误。
func (p *VaultProvider) Get(ctx context.Context, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("secrets: empty key name")
	}
	if err := validateVaultRelPath(trimmed); err != nil {
		return "", err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", ErrClosed
	}
	if p.path == "" {
		if value, ok := p.cachedLocked(trimmed); ok {
			return value, nil
		}
	}
	return p.lookupLocked(ctx, trimmed)
}

// GetBytes 读取逻辑密钥的字节值（证书等二进制材料请以 base64 存于 Vault）。
func (p *VaultProvider) GetBytes(ctx context.Context, name string) ([]byte, error) {
	value, err := p.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

// Close 释放空闲连接并清空缓存；可重复调用。
func (p *VaultProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.cache = nil
	p.objectFields = nil
	p.client.CloseIdleConnections()
	return nil
}

// lookupLocked 在持有 mu 的前提下读取远端。
//
// 持锁跨网络调用是刻意的：启动期只解析一次，串行化可避免瞬时并发把
// Vault 打满，也避免同一对象被并行拉取多次。
func (p *VaultProvider) lookupLocked(ctx context.Context, name string) (string, error) {
	if p.path != "" {
		fields, err := p.objectFieldsLocked(ctx)
		if err != nil {
			return "", err
		}
		value, ok := fields[name]
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return value, nil
	}

	relPath, err := joinVaultPath(p.prefix, name)
	if err != nil {
		return "", err
	}
	fields, err := p.readObjectLocked(ctx, relPath)
	if err != nil {
		if errors.Is(err, errVaultNotFound) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return "", err
	}
	value, ok := fields[p.valueField]
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: field %q in %s", ErrNotFound, p.valueField, name)
	}
	p.cacheValueLocked(name, value)
	return value, nil
}

// objectFieldsLocked 读取单对象模式的全部字段，并在 TTL 内复用。
func (p *VaultProvider) objectFieldsLocked(ctx context.Context) (map[string]string, error) {
	if p.cacheTTL >= 0 && p.objectFields != nil && time.Now().Before(p.objectExpires) {
		return p.objectFields, nil
	}
	fields, err := p.readObjectLocked(ctx, p.path)
	if err != nil {
		if errors.Is(err, errVaultNotFound) {
			return nil, fmt.Errorf("secrets: vault object %q not found; check mount/path: %w", p.path, err)
		}
		return nil, err
	}
	if p.cacheTTL >= 0 {
		p.objectFields = fields
		p.objectExpires = time.Now().Add(p.cacheTTL)
	}
	return fields, nil
}

// readObjectLocked 读取一个 KV 对象，首次调用自动探测 KV 版本。
func (p *VaultProvider) readObjectLocked(ctx context.Context, relPath string) (map[string]string, error) {
	if p.kvVersion != 0 {
		return p.readAtLocked(ctx, relPath, p.kvVersion)
	}

	fields, err := p.readAtLocked(ctx, relPath, 2)
	if err == nil {
		p.kvVersion = 2
		return fields, nil
	}
	if !errors.Is(err, errVaultNotFound) {
		return nil, err
	}

	fields, err = p.readAtLocked(ctx, relPath, 1)
	if err == nil {
		p.kvVersion = 1
		return fields, nil
	}
	return nil, err
}

// readAtLocked 按指定 KV 版本读取并解码。
func (p *VaultProvider) readAtLocked(ctx context.Context, relPath string, version int) (map[string]string, error) {
	apiPath := vaultAPIPath(p.mount, relPath, version)
	body, err := p.getLocked(ctx, apiPath)
	if err != nil {
		return nil, err
	}
	fields, err := decodeVaultFields(body, version)
	if err != nil {
		return nil, err
	}
	p.logger.Info("secrets: vault object read",
		"mount", p.mount,
		"path", relPath,
		"kv_version", version,
		"fields", len(fields))
	return fields, nil
}

// getLocked 发起一次带令牌的 GET，并把状态码翻译为错误语义。
func (p *VaultProvider) getLocked(ctx context.Context, apiPath string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint(apiPath), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("secrets: build vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", p.token)
	if p.namespace != "" {
		req.Header.Set("X-Vault-Namespace", p.namespace)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		// httpclient 的错误链只含目标地址与网络原因，不含请求头。
		p.logger.Warn("secrets: vault request failed", "path", apiPath, "error", err)
		return nil, fmt.Errorf("secrets: vault request %s: %w", apiPath, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxVaultResponseBytes))
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxVaultResponseBytes))
		if readErr != nil {
			return nil, fmt.Errorf("secrets: read vault response: %w", readErr)
		}
		return body, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", errVaultNotFound, apiPath)
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxVaultErrorSnippet))
		p.logger.Warn("secrets: vault request rejected", "path", apiPath, "status", resp.StatusCode)
		return nil, fmt.Errorf("secrets: vault returned %d for %s%s",
			resp.StatusCode, apiPath, formatVaultSnippet(snippet))
	}
}

// endpoint 拼接 Vault HTTP API 地址，保留地址中的既有前缀路径。
func (p *VaultProvider) endpoint(apiPath string) string {
	u := *p.baseURL
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/" + apiPath
	return u.String()
}

// cachedLocked 命中未过期缓存时返回值。
func (p *VaultProvider) cachedLocked(name string) (string, bool) {
	if p.cacheTTL < 0 {
		return "", false
	}
	entry, ok := p.cache[name]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expires) {
		delete(p.cache, name)
		return "", false
	}
	return entry.value, true
}

// cacheValueLocked 缓存逐键模式的单个取值。
func (p *VaultProvider) cacheValueLocked(name, value string) {
	if p.cacheTTL < 0 {
		return
	}
	p.cache[name] = vaultCacheEntry{value: value, expires: time.Now().Add(p.cacheTTL)}
}

// vaultAPIPath 按 KV 版本拼接 API 路径：v2 需插入 data 段。
func vaultAPIPath(mount, relPath string, version int) string {
	if version == 2 {
		return mount + "/data/" + relPath
	}
	return mount + "/" + relPath
}

// decodeVaultFields 解析 KV 响应；v2 需再解一层 data.data。
//
// 解析失败时不回显响应体：响应体就是密钥本身，进入错误链即泄露（SEC-05）。
func decodeVaultFields(body []byte, version int) (map[string]string, error) {
	var payload struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []string                   `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("secrets: decode vault response: invalid JSON")
	}
	if payload.Data == nil {
		return nil, errors.New("secrets: vault response has no data")
	}

	fields := payload.Data
	if version == 2 {
		inner, ok := payload.Data["data"]
		if !ok {
			return nil, errors.New("secrets: vault KV v2 response missing data.data")
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(inner, &decoded); err != nil {
			return nil, errors.New("secrets: vault KV v2 response data.data is not an object")
		}
		fields = decoded
	}

	out := make(map[string]string, len(fields))
	for name, raw := range fields {
		value, err := jsonScalar(raw)
		if err != nil {
			return nil, fmt.Errorf("secrets: field %q: %w", name, err)
		}
		out[name] = value
	}
	return out, nil
}

// jsonScalar 把 JSON 标量转成字符串；对象与数组视为配置错误。
func jsonScalar(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", errors.New("value is null")
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err == nil {
		return number.String(), nil
	}
	var boolean bool
	if err := json.Unmarshal(trimmed, &boolean); err == nil {
		return strconv.FormatBool(boolean), nil
	}
	return "", errors.New("value is not a scalar")
}

// joinVaultPath 拼接逐键模式的对象路径。
func joinVaultPath(prefix, name string) (string, error) {
	trimmedName := strings.Trim(strings.TrimSpace(name), "/")
	if trimmedName == "" {
		return "", errors.New("secrets: empty key name")
	}
	trimmedPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	if trimmedPrefix == "" {
		return trimmedName, nil
	}
	return trimmedPrefix + "/" + trimmedName, nil
}

// validateVaultRelPath 只放行 KV 路径允许的字符，拒绝穿越与绝对路径。
func validateVaultRelPath(path string) error {
	if path == "" {
		return errors.New("secrets: empty vault path")
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return fmt.Errorf("secrets: invalid vault path %q", path)
	}
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/':
		default:
			return fmt.Errorf("secrets: invalid vault path %q", path)
		}
	}
	return nil
}

// resolveVaultToken 按 显式令牌 → 令牌文件 → 环境变量 的顺序取令牌。
//
// 顺序即优先级：部署侧用文件/env 注入，显式令牌只留给本地调试。
func resolveVaultToken(cfg VaultConfig) (string, error) {
	if token := strings.TrimSpace(cfg.Token); token != "" {
		return token, nil
	}
	if path := strings.TrimSpace(cfg.TokenFile); path != "" {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: 路径来自部署配置的 token_file，非外部输入。
		if err != nil {
			return "", fmt.Errorf("secrets: read vault token file: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", errors.New("secrets: vault token file is empty")
		}
		return token, nil
	}
	envName := strings.TrimSpace(cfg.TokenEnv)
	if envName == "" {
		envName = defaultVaultTokenEnv
	}
	if token := strings.TrimSpace(os.Getenv(envName)); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("secrets: vault token is required (set %s, token_file, or token)", envName)
}

// formatVaultSnippet 格式化 Vault 错误片段；只用于非 2xx 响应（不含密钥值）。
func formatVaultSnippet(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if text == "" {
		return ""
	}
	if len(text) > maxVaultErrorSnippet {
		text = text[:maxVaultErrorSnippet] + "…"
	}
	return ": " + text
}

// newVaultHTTPClient 构造带超时与重试的客户端；配置 CA 时使用独立 Transport。
func newVaultHTTPClient(timeout time.Duration, caFile string) (*httpclient.Client, error) {
	cfg := httpclient.Config{
		Timeout:   timeout,
		UserAgent: "admin-frame-secrets/1.0",
	}
	if caFile != "" {
		transport, err := newVaultTransport(caFile, timeout)
		if err != nil {
			return nil, err
		}
		cfg.Transport = transport
	}
	client, err := httpclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("secrets: build vault http client: %w", err)
	}
	return client, nil
}

// newVaultTransport 构造信任自定义 CA 的 Transport。
//
// 自定义 Transport 会绕过 httpclient 的默认值，故超时参数必须在此显式设置。
func newVaultTransport(caFile string, timeout time.Duration) (*http.Transport, error) {
	pem, err := os.ReadFile(caFile) //nolint:gosec // G304: 路径来自部署配置的 ca_file，非外部输入。
	if err != nil {
		return nil, fmt.Errorf("secrets: read vault ca file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("secrets: vault ca file contains no valid certificate")
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		},
	}, nil
}

package secrets

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/httpclient"
)

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

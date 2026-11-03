package secrets_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/secrets"
)

// discardLogger 关闭访问日志，避免单测输出噪声。
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// newVaultServer 启动一个记录请求数与路径的假 Vault。
func newVaultServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// writeVaultJSON 返回 KV v2 单对象响应。
const vaultKV2Object = `{"data":{"data":{"db_dsn":"dsn-from-vault","auth_jwt_secret":"jwt-from-vault"},` +
	`"metadata":{"version":3}}}`

// TestVaultProviderSingleObjectCachesAll 验证单对象模式：一次请求覆盖全部密钥，
// 命中缓存后不再重复拉取，字段缺失映射为 ErrNotFound。
func TestVaultProviderSingleObjectCachesAll(t *testing.T) {
	const token = "s.test-token"
	server, hits := newVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got != token {
			t.Errorf("X-Vault-Token = %q, want token", got)
		}
		if r.URL.Path != "/v1/secret/data/admin-frame/prod" {
			t.Errorf("path = %q, want /v1/secret/data/admin-frame/prod", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(vaultKV2Object))
	})

	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   token,
		Mount:   "secret",
		Path:    "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	for name, want := range map[string]string{ //nolint:gosec // G101: 测试用占位取值，非真实凭据。
		"db_dsn":          "dsn-from-vault",
		"auth_jwt_secret": "jwt-from-vault",
	} {
		got, getErr := provider.Get(context.Background(), name)
		if getErr != nil {
			t.Fatalf("Get(%s) error = %v", name, getErr)
		}
		if got != want {
			t.Fatalf("Get(%s) = %q, want %q", name, got, want)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (单对象模式必须只发一次请求)", got)
	}

	if _, err := provider.Get(context.Background(), "absent_key"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(absent_key) error = %v, want ErrNotFound", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (缺字段不应触发远端请求)", got)
	}
}

// TestVaultProviderPerKeyModeFallsBackToKV1 验证逐键模式：v2 探测 404 后自动退回 v1，
// 并从 value 字段取值。
func TestVaultProviderPerKeyModeFallsBackToKV1(t *testing.T) {
	server, hits := newVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/secret/data/admin-frame/prod/db_dsn":
			w.WriteHeader(http.StatusNotFound)
		case "/v1/secret/admin-frame/prod/db_dsn":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"value":"v1-dsn"}}`))
		case "/v1/secret/admin-frame/prod/redis_password":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"value":"v1-redis"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Prefix:  "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	dsn, err := provider.Get(context.Background(), "db_dsn")
	if err != nil {
		t.Fatalf("Get(db_dsn) error = %v", err)
	}
	if dsn != "v1-dsn" {
		t.Fatalf("Get(db_dsn) = %q, want v1-dsn", dsn)
	}

	redis, err := provider.Get(context.Background(), "redis_password")
	if err != nil {
		t.Fatalf("Get(redis_password) error = %v", err)
	}
	if redis != "v1-redis" {
		t.Fatalf("Get(redis_password) = %q, want v1-redis", redis)
	}
	// 探测 2 次（v2 + v1）+ 第二个密钥 1 次；版本被记忆后不再重复探测。
	if got := hits.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}

	raw, err := provider.GetBytes(context.Background(), "db_dsn")
	if err != nil {
		t.Fatalf("GetBytes(db_dsn) error = %v", err)
	}
	if string(raw) != "v1-dsn" {
		t.Fatalf("GetBytes(db_dsn) = %q", raw)
	}
}

// TestVaultProviderMissingKeyIsNotFound 验证逐键模式下 404 翻译为 ErrNotFound，
// 使 Chain 能继续尝试下一个来源（这是「逐键」与「整对象」的语义差别）。
func TestVaultProviderMissingKeyIsNotFound(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Prefix:  "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	if _, err := provider.Get(context.Background(), "db_dsn"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// TestVaultProviderMissingObjectFailsFast 验证单对象模式：配置的对象不存在属部署错误，
// 必须返回硬错误而不是 ErrNotFound —— 否则「路径写错」会被静默降级成「密钥都不存在」。
func TestVaultProviderMissingObjectFailsFast(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Path:    "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	_, err = provider.Get(context.Background(), "db_dsn")
	if err == nil {
		t.Fatal("Get() error = nil, want hard error")
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get() error = %v, must not be ErrNotFound", err)
	}
}

// TestVaultProviderForbiddenDoesNotFallBack 验证 403 是硬错误：
// Chain 只在 ErrNotFound 时继续，鉴权失败必须中止而不是回落到下一个来源。
func TestVaultProviderForbiddenDoesNotFallBack(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Path:    "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	t.Setenv("APP_SECRET_DB_DSN", "dsn-from-env")
	envProvider := secrets.NewEnvProvider("APP_SECRET_")
	chain, chainErr := secrets.NewChain(provider, envProvider)
	if chainErr != nil {
		t.Fatalf("NewChain() error = %v", chainErr)
	}

	value, getErr := chain.Get(context.Background(), "db_dsn")
	if getErr == nil {
		t.Fatalf("Chain.Get() = %q, want error (禁止回落到环境变量)", value)
	}
	if errors.Is(getErr, secrets.ErrNotFound) {
		t.Fatalf("Chain.Get() error = %v, want hard error", getErr)
	}
}

// TestVaultProviderTokenFile 验证令牌文件注入（K8s 投影 / Vault Agent 场景）。
func TestVaultProviderTokenFile(t *testing.T) {
	const token = "s.token-from-file" //nolint:gosec // G101: 测试用占位令牌，非真实凭据。
	server, _ := newVaultServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got != token {
			t.Errorf("X-Vault-Token = %q, want file token", got)
		}
		_, _ = w.Write([]byte(vaultKV2Object))
	})

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address:   server.URL,
		TokenFile: tokenPath,
		Path:      "admin-frame/prod",
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	if _, err := provider.Get(context.Background(), "db_dsn"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}

// TestVaultProviderInvalidJSONDoesNotLeakBody 验证解析失败时不回显响应体：
// 响应体本身可能就是密钥，进入错误链即泄露（SEC-05）。
func TestVaultProviderInvalidJSONDoesNotLeakBody(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("SUPER-SECRET-VALUE not json"))
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Path:    "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	_, getErr := provider.Get(context.Background(), "db_dsn")
	if getErr == nil {
		t.Fatal("Get() error = nil, want decode error")
	}
	if strings.Contains(getErr.Error(), "SUPER-SECRET-VALUE") {
		t.Fatalf("Get() error leaks response body: %v", getErr)
	}
}

// TestVaultProviderTimeoutFailsFast 验证 Vault 不响应时快速失败，而不是挂住启动。
func TestVaultProviderTimeoutFailsFast(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(vaultKV2Object))
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Path:    "admin-frame/prod",
		Timeout: 30 * time.Millisecond,
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	defer func() { _ = provider.Close() }()

	start := time.Now()
	if _, getErr := provider.Get(context.Background(), "db_dsn"); getErr == nil {
		t.Fatal("Get() error = nil, want timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Get() took %v, want fail fast", elapsed)
	}
}

// TestVaultProviderCloseIsIdempotent 验证关闭语义：可重复调用，关闭后读拒绝。
func TestVaultProviderCloseIsIdempotent(t *testing.T) {
	server, _ := newVaultServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vaultKV2Object))
	})
	provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
		Address: server.URL,
		Token:   "s.token",
		Path:    "admin-frame/prod",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := provider.Get(context.Background(), "db_dsn"); !errors.Is(err, secrets.ErrClosed) {
		t.Fatalf("Get() after Close error = %v, want ErrClosed", err)
	}
}

// TestVaultProviderRejectsInvalidConfig 验证构造期就拦下配置错误。
func TestVaultProviderRejectsInvalidConfig(t *testing.T) {
	cases := map[string]secrets.VaultConfig{
		"缺少地址":          {Token: "s.token", Path: "admin-frame/prod"},
		"非 http scheme": {Address: "vault://127.0.0.1:8200", Token: "s.token", Path: "admin-frame/prod"},
		"缺少路径与前缀":       {Address: "http://127.0.0.1:8200", Token: "s.token"},
		"路径与前缀并存":       {Address: "http://127.0.0.1:8200", Token: "s.token", Path: "a/b", Prefix: "a"},
		"路径穿越":          {Address: "http://127.0.0.1:8200", Token: "s.token", Path: "../etc/passwd"},
		"非法字符":          {Address: "http://127.0.0.1:8200", Token: "s.token", Path: "a b"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			cfg.Logger = discardLogger()
			if _, err := secrets.NewVaultProvider(cfg); err == nil {
				t.Fatal("NewVaultProvider() error = nil, want config error")
			}
		})
	}

	t.Run("缺少令牌", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "")
		_, err := secrets.NewVaultProvider(secrets.VaultConfig{ //nolint:gosec // G101: 变量名不是凭据。
			Address:  "http://127.0.0.1:8200",
			Path:     "admin-frame/prod",
			TokenEnv: "VAULT_TOKEN_UNSET_FOR_TEST",
			Logger:   discardLogger(),
		})
		if err == nil {
			t.Fatal("NewVaultProvider() error = nil, want missing token error")
		}
	})
}

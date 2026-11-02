package secrets_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform/secrets"
)

// TestEnvProviderGet 验证密钥名到环境变量名的映射与缺失语义。
func TestEnvProviderGet(t *testing.T) {
	provider := secrets.NewEnvProvider("APP_SECRET_")
	if got := provider.EnvName("db/dsn"); got != "APP_SECRET_DB_DSN" {
		t.Fatalf("EnvName(db/dsn) = %q, want APP_SECRET_DB_DSN", got)
	}
	if got := provider.EnvName("jwt-signing-key"); got != "APP_SECRET_JWT_SIGNING_KEY" {
		t.Fatalf("EnvName(jwt-signing-key) = %q", got)
	}

	const dsn = "mysql://app:placeholder@db:3306/app" //nolint:gosec // G101: 测试用占位 DSN，非真实凭据。
	t.Setenv("APP_SECRET_DB_DSN", dsn)
	value, err := provider.Get(context.Background(), "db/dsn")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != dsn {
		t.Fatalf("Get() = %q, want dsn", value)
	}

	if _, err := provider.Get(context.Background(), "absent"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(absent) error = %v, want ErrNotFound", err)
	}

	t.Setenv("APP_SECRET_EMPTY", "   ")
	if _, err := provider.Get(context.Background(), "empty"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(empty) error = %v, want ErrNotFound", err)
	}
}

// TestFileProviderReadsTrimmedValue 验证文件读取去尾随换行与二进制读取。
func TestFileProviderReadsTrimmedValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "db", "dsn"), []byte("dsn-value\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	provider := secrets.NewFileProvider(dir)

	value, err := provider.Get(context.Background(), "db/dsn")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "dsn-value" {
		t.Fatalf("Get() = %q, want trailing newline trimmed", value)
	}

	raw, err := provider.GetBytes(context.Background(), "db/dsn")
	if err != nil {
		t.Fatalf("GetBytes() error = %v", err)
	}
	if string(raw) != "dsn-value\n" {
		t.Fatalf("GetBytes() = %q, want raw content", raw)
	}
}

// TestFileProviderRejectsTraversal 验证目录穿越与非法名被拒绝。
func TestFileProviderRejectsTraversal(t *testing.T) {
	provider := secrets.NewFileProvider(t.TempDir())
	for _, name := range []string{"../etc/passwd", "/etc/passwd", "..", "a/../../b"} {
		if _, err := provider.Get(context.Background(), name); err == nil {
			t.Fatalf("Get(%q): want error, got nil", name)
		} else if errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("Get(%q) error = %v, want invalid-name error", name, err)
		}
	}
}

// TestChainFallsThroughOnNotFound 验证链式查找：命中即返回，非 NotFound 立即失败。
func TestChainFallsThroughOnNotFound(t *testing.T) {
	t.Setenv("APP_SECRET_TOKEN", "from-env")
	chain, err := secrets.NewChain(
		secrets.NewFileProvider(t.TempDir()),
		secrets.NewEnvProvider("APP_SECRET_"),
	)
	if err != nil {
		t.Fatalf("NewChain() error = %v", err)
	}
	value, err := chain.Get(context.Background(), "token")
	if err != nil {
		t.Fatalf("Chain.Get() error = %v", err)
	}
	if value != "from-env" {
		t.Fatalf("Chain.Get() = %q, want from-env", value)
	}

	if _, err := chain.Get(context.Background(), "missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Chain.Get(missing) error = %v, want ErrNotFound", err)
	}

	if _, err := secrets.NewChain(); err == nil {
		t.Fatal("NewChain() empty: want error, got nil")
	}
}

// TestResolverRequireFailsFast 验证启动期批量取密钥：缺失即失败且错误只含密钥名。
func TestResolverRequireFailsFast(t *testing.T) {
	t.Setenv("APP_SECRET_DB_DSN", "super-secret-dsn")
	t.Setenv("APP_SECRET_JWT_KEY", "super-secret-jwt")
	resolver, err := secrets.NewResolver(secrets.NewEnvProvider("APP_SECRET_"))
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	values, err := resolver.Require(context.Background(), "db/dsn", "jwt/key")
	if err != nil {
		t.Fatalf("Require() error = %v", err)
	}
	if values["db/dsn"] != "super-secret-dsn" || values["jwt/key"] != "super-secret-jwt" {
		t.Fatalf("Require() values = %+v", values)
	}

	_, err = resolver.Require(context.Background(), "db/dsn", "missing/a", "missing/b")
	if err == nil {
		t.Fatal("Require() with missing keys: want error, got nil")
	}
	if !strings.Contains(err.Error(), "missing/a, missing/b") {
		t.Fatalf("Require() error = %v, want sorted missing keys", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("Require() error leaks secret value: %v", err)
	}
}

// TestProviderClosed 验证关闭后拒绝读取。
func TestProviderClosed(t *testing.T) {
	provider := secrets.NewEnvProvider("APP_SECRET_")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := provider.Get(context.Background(), "token"); !errors.Is(err, secrets.ErrClosed) {
		t.Fatalf("Get() after Close error = %v, want ErrClosed", err)
	}
}

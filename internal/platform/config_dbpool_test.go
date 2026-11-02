package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// baseDBPoolConfig 返回一份仅 db 段可变的最小合法配置。
func baseDBPoolConfig(db platform.DBConfig) *platform.Config {
	return &platform.Config{
		Service: "admin-api",
		Channel: "admin",
		Addr:    ":8080",
		Auth:    platform.AuthConfig{ExpectedChannel: "admin"},
		DB:      db,
		Log:     platform.LogConfig{Level: "info", Format: "json"},
	}
}

// TestValidateDBPoolParams 覆盖连接池参数的边界校验。
//
// 这三条规则都是「静默错误」：负值让 database/sql 走到未定义分支，idle 超过 open
// 会被静默截断，均不会在运行时报错，只能在启动校验阶段拦住。
func TestValidateDBPoolParams(t *testing.T) {
	const dsn = "root:root@tcp(localhost:3306)/admin_frame?parseTime=true"
	cases := []struct {
		name    string
		pool    platform.DBConfig
		wantErr string
	}{
		{
			name: "defaults_zero",
			pool: platform.DBConfig{Driver: "mysql", DSN: dsn},
		},
		{
			name: "explicit_valid",
			pool: platform.DBConfig{
				Driver: "mysql", DSN: dsn,
				MaxOpenConns: 10, MaxIdleConns: 5, ConnMaxLifetime: 10 * time.Minute,
			},
		},
		{
			name: "idle_equals_open",
			pool: platform.DBConfig{Driver: "mysql", DSN: dsn, MaxOpenConns: 8, MaxIdleConns: 8},
		},
		{
			name:    "negative_open",
			pool:    platform.DBConfig{Driver: "mysql", DSN: dsn, MaxOpenConns: -1},
			wantErr: "db.max_open_conns must not be negative",
		},
		{
			name:    "negative_idle",
			pool:    platform.DBConfig{Driver: "mysql", DSN: dsn, MaxIdleConns: -2},
			wantErr: "db.max_idle_conns must not be negative",
		},
		{
			name:    "negative_lifetime",
			pool:    platform.DBConfig{Driver: "mysql", DSN: dsn, ConnMaxLifetime: -time.Second},
			wantErr: "db.conn_max_lifetime must not be negative",
		},
		{
			name:    "idle_exceeds_open",
			pool:    platform.DBConfig{Driver: "mysql", DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 9},
			wantErr: "db.max_idle_conns (9) must not exceed db.max_open_conns (4)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := baseDBPoolConfig(tc.pool).Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

// TestLoadConfigParsesDBPool 校验 yaml 键名与时间单位解析，避免配置写了不生效。
func TestLoadConfigParsesDBPool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.yaml")
	content := []byte(`
service: admin-api
channel: admin
addr: ":8080"
auth:
  expected_channel: admin
db:
  driver: mysql
  dsn: "root:root@tcp(localhost:3306)/admin_frame?parseTime=true"
  max_open_conns: 6
  max_idle_conns: 3
  conn_max_lifetime: 15m
log:
  level: info
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.MaxOpenConns != 6 {
		t.Fatalf("max_open_conns=%d, want 6", cfg.DB.MaxOpenConns)
	}
	if cfg.DB.MaxIdleConns != 3 {
		t.Fatalf("max_idle_conns=%d, want 3", cfg.DB.MaxIdleConns)
	}
	if cfg.DB.ConnMaxLifetime != 15*time.Minute {
		t.Fatalf("conn_max_lifetime=%s, want 15m", cfg.DB.ConnMaxLifetime)
	}
}

// TestLoadConfigDBPoolEnvOverride 确认环境变量可覆盖 yaml（BindEnv 按字段路径派生键名）。
func TestLoadConfigDBPoolEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.yaml")
	content := []byte(`
service: admin-api
channel: admin
addr: ":8080"
auth:
  expected_channel: admin
db:
  driver: mysql
  dsn: "root:root@tcp(localhost:3306)/admin_frame?parseTime=true"
  max_open_conns: 6
log:
  level: info
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_FRAME_DB_MAX_OPEN_CONNS", "12")
	t.Setenv("ADMIN_FRAME_DB_MAX_IDLE_CONNS", "4")

	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.MaxOpenConns != 12 || cfg.DB.MaxIdleConns != 4 {
		t.Fatalf("pool=%d/%d, want 12/4", cfg.DB.MaxOpenConns, cfg.DB.MaxIdleConns)
	}
}

// TestLoadDBConfigKeepsPoolParams 保证 cmd/migrate 与业务进程读到同一组池参数。
func TestLoadDBConfigKeepsPoolParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate.yaml")
	content := []byte("db:\n  driver: mysql\n  dsn: \"file-dsn\"\n  max_open_conns: 2\n  conn_max_lifetime: 1m\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := platform.LoadDBConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxOpenConns != 2 || got.ConnMaxLifetime != time.Minute {
		t.Fatalf("got=%+v", got)
	}
}

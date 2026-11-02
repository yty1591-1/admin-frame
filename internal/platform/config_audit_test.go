package platform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// auditBaseConfig 是审计用例的公共头部；每个用例自行拼接 db 段与 audit 段，
// 避免重复 mapping key 导致 YAML 解析失败。
const auditBaseConfig = "service: admin-api\nchannel: admin\naddr: \":8081\"\n"

// mysqlBlock 返回持久库配置（audit.store=db 的前提）。
func mysqlBlock() string {
	return "db:\n  driver: mysql\n  dsn: \"user:pass@tcp(127.0.0.1:3306)/admin_frame\"\n"
}

// memoryBlock 返回内存库配置（无需 DSN）。
func memoryBlock() string {
	return "db:\n  driver: memory\n"
}

// loadAuditConfig 写入并加载配置，走真实「defaults → Validate」路径。
func loadAuditConfig(t *testing.T, yaml string) (*platform.Config, error) {
	t.Helper()
	path := writeConfig(t, "audit.yaml", auditBaseConfig+yaml)
	return platform.LoadConfig(path)
}

// TestAuditDefaults 验证审计默认值：落库、180 天保留、31 天查询窗口、24h 清理。
func TestAuditDefaults(t *testing.T) {
	cfg, err := loadAuditConfig(t, mysqlBlock())
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := cfg.Audit.EffectiveStore(cfg.DB.Driver); got != platform.AuditStoreDB {
		t.Fatalf("EffectiveStore = %q, want %q", got, platform.AuditStoreDB)
	}
	if cfg.Audit.RetentionDays != platform.AuditMinRetentionDays {
		t.Fatalf("RetentionDays = %d, want %d", cfg.Audit.RetentionDays, platform.AuditMinRetentionDays)
	}
	if cfg.Audit.Query.MaxRangeDays != 31 {
		t.Fatalf("Query.MaxRangeDays = %d, want 31", cfg.Audit.Query.MaxRangeDays)
	}
	if cfg.Audit.Query.MaxPageSize != 100 {
		t.Fatalf("Query.MaxPageSize = %d, want 100 (API-05)", cfg.Audit.Query.MaxPageSize)
	}
	if cfg.Audit.Retention.CleanupInterval != 24*time.Hour {
		t.Fatalf("CleanupInterval = %s, want 24h", cfg.Audit.Retention.CleanupInterval)
	}
}

// TestAuditStoreResolution 验证 auto 按驱动解析为 log，以及 db 与内存库冲突被拒绝。
func TestAuditStoreResolution(t *testing.T) {
	cfg, err := loadAuditConfig(t, memoryBlock()+"audit:\n  store: auto\n")
	if err != nil {
		t.Fatalf("LoadConfig() memory+auto error = %v", err)
	}
	if got := cfg.Audit.EffectiveStore(cfg.DB.Driver); got != platform.AuditStoreLog {
		t.Fatalf("EffectiveStore = %q, want log for memory db", got)
	}

	if _, err := loadAuditConfig(t, memoryBlock()+"audit:\n  store: db\n"); err == nil {
		t.Fatal("LoadConfig() memory+db: want error, got nil")
	}
}

// TestAuditProdRejectsLogStore 验证生产禁止只写日志（AUD-06）。
func TestAuditProdRejectsLogStore(t *testing.T) {
	yaml := "env: prod\n" + "log:\n  format: json\n" + memoryBlock() + "audit:\n  store: log\n"
	_, err := loadAuditConfig(t, yaml)
	if err == nil {
		t.Fatal("LoadConfig() prod+log: want error, got nil")
	}
	if !strings.Contains(err.Error(), "audit.store must resolve to db") {
		t.Fatalf("error = %v, want audit.store prod rule", err)
	}
}

// TestAuditValidationBounds 覆盖保留期、范围、导出与批量参数的边界校验。
func TestAuditValidationBounds(t *testing.T) {
	tests := []struct {
		name    string
		audit   string
		wantErr string
	}{
		{
			name:    "retention below baseline",
			audit:   "audit:\n  retention_days: 30\n",
			wantErr: "audit.retention_days",
		},
		{
			name:    "retention above ceiling",
			audit:   "audit:\n  retention_days: 5000\n",
			wantErr: "audit.retention_days",
		},
		{
			name:    "query range too large",
			audit:   "audit:\n  query:\n    max_range_days: 400\n",
			wantErr: "audit.query.max_range_days",
		},
		{
			name:    "page size above API max",
			audit:   "audit:\n  query:\n    max_page_size: 500\n",
			wantErr: "audit.query.max_page_size",
		},
		{
			name:    "export rows negative",
			audit:   "audit:\n  export:\n    max_rows: -1\n",
			wantErr: "audit.export.max_rows",
		},
		{
			name:    "batch size too small",
			audit:   "audit:\n  retention:\n    batch_size: 10\n",
			wantErr: "audit.retention.batch_size",
		},
		{
			name:    "cleanup interval too short",
			audit:   "audit:\n  retention:\n    cleanup_interval: 1s\n",
			wantErr: "audit.retention.cleanup_interval",
		},
		{
			name:    "unsupported store",
			audit:   "audit:\n  store: s3\n",
			wantErr: "unsupported audit.store",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadAuditConfig(t, mysqlBlock()+tc.audit)
			if err == nil {
				t.Fatalf("LoadConfig() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadConfig() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

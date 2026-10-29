package identityruntime

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// TestNewFailsFastWithoutDatabase 验证 db.driver 非 memory 时缺失连接直接失败（IDN-P2-01）。
func TestNewFailsFastWithoutDatabase(t *testing.T) {
	cfg := &platform.Config{
		Channel:  "admin",
		Identity: platform.IdentityConfig{Enabled: true},
		DB:       platform.DBConfig{Driver: "mysql"},
	}
	if _, err := New(&platform.Deps{Config: cfg}); err == nil ||
		!strings.Contains(err.Error(), "no database connection is available") {
		t.Fatalf("err=%v want fail-fast without database", err)
	}
}

// TestPasswordPolicyFromConfig 验证配置策略映射与零值默认行为（IDN-P2-01）。
func TestPasswordPolicyFromConfig(t *testing.T) {
	empty := passwordPolicyFromConfig(&platform.Config{})
	fallback := app.DefaultPasswordPolicy()
	if empty != fallback {
		t.Fatalf("policy = %+v, want default %+v", empty, fallback)
	}

	requireComplexity := false
	cfg := &platform.Config{}
	cfg.Identity.Password.MinLength = 10
	cfg.Identity.Password.MaxLength = 64
	cfg.Identity.Password.RequireComplexity = &requireComplexity
	policy := passwordPolicyFromConfig(cfg)
	if policy.MinLength != 10 || policy.MaxLength != 64 || policy.RequireComplexity {
		t.Fatalf("policy = %+v, want 10/64/false", policy)
	}
}

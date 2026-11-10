package appconfig_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
)

// m2mBaseConfig 返回启用身份域且 M2M 配置合法的本地配置。
func m2mBaseConfig() *appconfig.Config {
	cfg := mfaBaseConfig()
	cfg.Identity.M2M = identityconfig.IdentityM2MConfig{
		Enabled:       true,
		TokenTTL:      15 * time.Minute,
		RotateGrace:   24 * time.Hour,
		MaxSecretAge:  2160 * time.Hour,
		AllowedScopes: []string{"identity:account:read", "audit:read"},
	}
	return cfg
}

// TestValidateM2MAcceptsBaseline 验证合法 M2M 配置可启动。
func TestValidateM2MAcceptsBaseline(t *testing.T) {
	if err := m2mBaseConfig().Validate(); err != nil {
		t.Fatalf("baseline m2m config: %v", err)
	}
}

// TestValidateM2MRejectsBadTTL 验证令牌有效期越界启动失败（§10.2 M2M 时长）。
func TestValidateM2MRejectsBadTTL(t *testing.T) {
	for _, ttl := range []time.Duration{time.Minute, 2 * time.Hour} {
		cfg := m2mBaseConfig()
		cfg.Identity.M2M.TokenTTL = ttl
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "identity.m2m.token_ttl") {
			t.Fatalf("ttl=%s: err = %v, want token_ttl rejection", ttl, err)
		}
	}
}

// TestValidateM2MRejectsSecretAgeNotGreaterThanGrace 验证 max_secret_age 必须大于宽限期。
func TestValidateM2MRejectsSecretAgeNotGreaterThanGrace(t *testing.T) {
	cfg := m2mBaseConfig()
	cfg.Identity.M2M.RotateGrace = 168 * time.Hour
	cfg.Identity.M2M.MaxSecretAge = 168 * time.Hour
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "max_secret_age must be greater") {
		t.Fatalf("err = %v, want max_secret_age rejection", err)
	}
}

// TestValidateM2MRejectsEmptyScopes 验证开启 M2M 时 allowed_scopes 必填。
func TestValidateM2MRejectsEmptyScopes(t *testing.T) {
	cfg := m2mBaseConfig()
	cfg.Identity.M2M.AllowedScopes = nil
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "allowed_scopes is required") {
		t.Fatalf("err = %v, want allowed_scopes rejection", err)
	}
}

// TestValidateM2MRejectsWildcardScope 验证通配符权限码启动失败。
func TestValidateM2MRejectsWildcardScope(t *testing.T) {
	cfg := m2mBaseConfig()
	cfg.Identity.M2M.AllowedScopes = []string{"identity:*"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "must not contain wildcard") {
		t.Fatalf("err = %v, want wildcard rejection", err)
	}
}

// TestValidateM2MRejectsUnknownPermission 验证不在 IAM 目录内的权限码启动失败（装配期校验）。
func TestValidateM2MRejectsUnknownPermission(t *testing.T) {
	cfg := m2mBaseConfig()
	cfg.Identity.M2M.AllowedScopes = []string{"identity:nonexistent:read"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown permission code") {
		t.Fatalf("err = %v, want unknown permission rejection", err)
	}
}

// TestValidateM2MRequiresIdentityEnabled 验证 m2m.enabled 依赖 identity.enabled。
func TestValidateM2MRequiresIdentityEnabled(t *testing.T) {
	cfg := m2mBaseConfig()
	cfg.Identity.Enabled = false
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires identity.enabled=true") {
		t.Fatalf("err = %v, want identity.enabled dependency rejection", err)
	}
}

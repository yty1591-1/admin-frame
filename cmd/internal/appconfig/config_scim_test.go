package appconfig_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
)

// scimBaseConfig 返回启用身份域且 SCIM 配置合法的本地配置（A 端）。
func scimBaseConfig() *appconfig.Config {
	cfg := mfaBaseConfig()
	cfg.Identity.SCIM = identityconfig.IdentitySCIMConfig{
		Enabled:        true,
		Provider:       "okta",
		PageSize:       50,
		MaxPayload:     1 << 20,
		RequestTimeout: 10 * time.Second,
		TokenGrace:     168 * time.Hour,
		GroupRoleMap:   map[string]string{"ops": "ops_admin"},
	}
	return cfg
}

// TestValidateSCIMAcceptsBaseline 验证合法 SCIM 配置可启动。
func TestValidateSCIMAcceptsBaseline(t *testing.T) {
	if err := scimBaseConfig().Validate(); err != nil {
		t.Fatalf("baseline scim config: %v", err)
	}
}

// TestValidateSCIMRejectsProvider 验证 provider 必填且格式受限（§10.2 SCIM 开关）。
func TestValidateSCIMRejectsProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider string
	}{
		{name: "missing", provider: ""},
		{name: "uppercase", provider: "Okta"},
		{name: "illegal char", provider: "okta corp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := scimBaseConfig()
			cfg.Identity.SCIM.Provider = tc.provider
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "identity.scim.provider") {
				t.Fatalf("err = %v, want provider rejection", err)
			}
		})
	}
}

// TestValidateSCIMRejectsNonAdminChannel 验证只有 A 端允许开启。
func TestValidateSCIMRejectsNonAdminChannel(t *testing.T) {
	cfg := scimBaseConfig()
	cfg.Channel = "biz"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "only supported on channel=admin") {
		t.Fatalf("err = %v, want admin-only rejection", err)
	}
}

// TestValidateSCIMRejectsGroupRoleMapWhenDisabled 验证关闭时不得配置映射。
func TestValidateSCIMRejectsGroupRoleMapWhenDisabled(t *testing.T) {
	cfg := mfaBaseConfig()
	cfg.Identity.SCIM = identityconfig.IdentitySCIMConfig{
		GroupRoleMap: map[string]string{"ops": "ops_admin"},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "group_role_map requires") {
		t.Fatalf("err = %v, want group_role_map rejection", err)
	}
}

// TestValidateSCIMRejectsBadRoleCode 验证角色 code 语法。
func TestValidateSCIMRejectsBadRoleCode(t *testing.T) {
	cfg := scimBaseConfig()
	cfg.Identity.SCIM.GroupRoleMap = map[string]string{"ops": "Ops Admin"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "group_role_map") {
		t.Fatalf("err = %v, want role code rejection", err)
	}
}

// TestValidateSCIMRejectsBadRanges 验证分页 / 请求体 / 超时 / 宽限范围。
func TestValidateSCIMRejectsBadRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*identityconfig.IdentitySCIMConfig)
		want   string
	}{
		{name: "page size too large", mutate: func(c *identityconfig.IdentitySCIMConfig) { c.PageSize = 101 }, want: "page_size"},
		{name: "payload too small", mutate: func(c *identityconfig.IdentitySCIMConfig) { c.MaxPayload = 1024 }, want: "max_payload"},
		{name: "timeout too long", mutate: func(c *identityconfig.IdentitySCIMConfig) { c.RequestTimeout = time.Minute }, want: "request_timeout"},
		{name: "grace too long", mutate: func(c *identityconfig.IdentitySCIMConfig) { c.TokenGrace = 1000 * time.Hour }, want: "token_grace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := scimBaseConfig()
			tc.mutate(&cfg.Identity.SCIM)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %s rejection", err, tc.want)
			}
		})
	}
}

// TestSCIMAutoProvisionDefaultsToTrue 验证未配置 auto_provision 时默认允许建号。
func TestSCIMAutoProvisionDefaultsToTrue(t *testing.T) {
	var cfg identityconfig.IdentitySCIMConfig
	if !cfg.AutoProvision() {
		t.Fatalf("auto provision must default to true")
	}
	disabled := false
	cfg.AutoProvisionPtr = &disabled
	if cfg.AutoProvision() {
		t.Fatalf("explicit false must be honored")
	}
}

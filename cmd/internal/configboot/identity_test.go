package configboot

import (
	"testing"
)

func TestWithProcessIdentity(t *testing.T) {
	got := WithProcessIdentity(map[string]string{
		"log.level": "info",
		"addr":      ":8080",
	}, ProcessIdentity{Service: "admin-api", Channel: "admin", Env: "staging", Addr: ":8081"})
	if got["service"] != "admin-api" || got["channel"] != "admin" {
		t.Fatalf("identity = %+v", got)
	}
	if got["env"] != "staging" {
		t.Fatalf("env = %q, want staging (must be visible to Validate)", got["env"])
	}
	if got["auth.expected_channel"] != "admin" {
		t.Fatalf("expected_channel = %q", got["auth.expected_channel"])
	}
	if got["addr"] != ":8081" || got["server.addr"] != ":8081" {
		t.Fatalf("addr override = %+v", got)
	}
	if got["log.level"] != "info" {
		t.Fatalf("pulled values lost: %+v", got)
	}
}

func TestSetIfEmpty(t *testing.T) {
	values := map[string]string{"auth.mode": "demo"}
	SetIfEmpty(values, "auth.mode", "jwt")
	SetIfEmpty(values, "auth.jwt.issuer", "admin-frame")
	if values["auth.mode"] != "demo" {
		t.Fatalf("must not override existing: %q", values["auth.mode"])
	}
	if values["auth.jwt.issuer"] != "admin-frame" {
		t.Fatalf("must fill empty: %q", values["auth.jwt.issuer"])
	}
}

func TestApplyProdLikeProcessDefaults(t *testing.T) {
	admin := map[string]string{}
	ApplyProdLikeProcessDefaults(admin, "prod", "admin")
	if admin["iam.mode"] != "enforce" || admin["iam.approval.enabled"] != "true" {
		t.Fatalf("admin iam: %+v", admin)
	}
	if admin["iam.cache.driver"] != "redis" || admin["auth.mode"] != "jwt" {
		t.Fatalf("admin auth/cache: %+v", admin)
	}

	c := map[string]string{
		"identity.sms.enabled":      "false",
		"identity.sms.driver":       "memory",
		"identity.sms.daily_budget": "0",
	}
	ApplyProdLikeProcessDefaults(c, "staging", "c")
	if c["identity.sms.enabled"] != "true" || c["identity.sms.driver"] != "aliyun" {
		t.Fatalf("c sms: %+v", c)
	}
	if c["identity.sms.daily_budget"] != "1000" || c["identity.sms.template_id"] != "bootstrap-placeholder" {
		t.Fatalf("c sms budget/template: %+v", c)
	}
	if c["iam.mode"] != "" {
		t.Fatalf("c must not set iam.mode: %+v", c)
	}
	if c["identity.mfa.admin_required"] != "false" {
		t.Fatalf("c must clear admin_required: %+v", c)
	}

	biz := map[string]string{"identity.mfa.admin_required": "true"}
	ApplyProdLikeProcessDefaults(biz, "prod", "biz")
	if biz["identity.mfa.admin_required"] != "false" {
		t.Fatalf("biz must clear admin_required: %+v", biz)
	}

	local := map[string]string{}
	ApplyProdLikeProcessDefaults(local, "dev", "admin")
	if len(local) != 0 {
		t.Fatalf("dev must be no-op: %+v", local)
	}

	existing := map[string]string{
		"iam.mode":             "shadow",
		"iam.approval.enabled": "false",
		"iam.cache.driver":     "memory",
	}
	ApplyProdLikeProcessDefaults(existing, "prod", "admin")
	if existing["iam.mode"] != "enforce" || existing["iam.approval.enabled"] != "true" || existing["iam.cache.driver"] != "redis" {
		t.Fatalf("admin/biz must force IAM prod gates over snapshot defaults: %+v", existing)
	}
}

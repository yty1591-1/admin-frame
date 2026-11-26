package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestRunInfraProbesSkipsDisabled(t *testing.T) {
	t.Parallel()
	results := app.RunInfraProbes(context.Background(), app.ProbeInput{
		Env: "dev",
		Values: map[string]string{
			"db.driver":                          "memory",
			"redis.addr":                         "",
			"storage.driver":                     "memory",
			"async.enabled":                      "false",
			"notification.channels.email.driver": "memory",
		},
	})
	if len(results) != 0 {
		t.Fatalf("disabled infra must skip probes, got %#v", results)
	}
}

func TestRunInfraProbesSMTPMissingSecret(t *testing.T) {
	t.Parallel()
	results := app.RunInfraProbes(context.Background(), app.ProbeInput{
		Env: "dev",
		Values: map[string]string{
			"notification.channels.email.driver":    "smtp",
			"notification.channels.email.smtp.host": "smtp.example.com",
			"notification.channels.email.smtp.auth": "plain",
		},
	})
	if len(results) != 1 || results[0].Reason != spec.GapReasonSecretMissing {
		t.Fatalf("want secret_missing for smtp password, got %#v", results)
	}
}

func TestRepairableProbeRefsDropsDeployEnv(t *testing.T) {
	t.Parallel()
	refs := app.RepairableProbeRefs([]app.ProbeResult{
		{Ref: "platform/db.dsn", Reason: spec.GapReasonDeployEnvMissing},
		{Ref: "platform/redis.addr", Reason: spec.GapReasonConnectivityFailed},
	})
	if len(refs) != 1 || refs[0] != "platform/redis.addr" {
		t.Fatalf("got %v", refs)
	}
}

func TestRunInfraProbesLocalStorageUsesDeployRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	results := app.RunInfraProbes(context.Background(), app.ProbeInput{
		Env: "prod",
		Values: map[string]string{
			"db.driver":                          "memory",
			"storage.driver":                     "local",
			"storage.local.root":                 dir,
			"async.enabled":                      "false",
			"notification.channels.email.driver": "memory",
		},
	})
	if len(results) != 0 {
		t.Fatalf("local storage with deploy root must pass, got %#v", results)
	}
}

func TestRunInfraProbesRedisRequiredMissingAddr(t *testing.T) {
	t.Parallel()
	results := app.RunInfraProbes(context.Background(), app.ProbeInput{
		Env: "prod",
		Values: map[string]string{
			"db.driver":                          "memory",
			"storage.driver":                     "memory",
			"idempotency.driver":                 "redis",
			"async.enabled":                      "false",
			"notification.channels.email.driver": "memory",
		},
	})
	if len(results) != 1 || results[0].Ref != "platform/redis.addr" ||
		results[0].Reason != spec.GapReasonMissingInSnapshot {
		t.Fatalf("want missing redis.addr, got %#v", results)
	}
}

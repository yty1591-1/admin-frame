package app_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/infrastructure/memory"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// schemaSpecs 覆盖向导需要区分的几类键：必填、有默认、枚举、时长、敏感。
func schemaSpecs(t *testing.T) *spec.Registry {
	t.Helper()
	registry := spec.NewRegistry()
	err := registry.Register(
		domain.KeySpec{
			Namespace: "platform", Key: "http.base_url", ValueType: domain.ValueTypeString,
			Required: true, Description: "对外基地址（含协议）", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "db.driver", ValueType: domain.ValueTypeString,
			Default: "memory", Description: "数据库驱动", Owner: "team-platform",
		},
		domain.KeySpec{
			Namespace: "platform", Key: "log.level", ValueType: domain.ValueTypeString,
			Default: "info", Description: "日志级别", Owner: "team-platform",
			Enum: []string{"debug", "info", "warn"},
		},
		domain.KeySpec{
			Namespace: "platform", Key: "http.timeout", ValueType: domain.ValueTypeDuration,
			Default: "10s", Description: "HTTP 超时", Owner: "team-platform", Reloadable: true,
		},
		domain.KeySpec{
			Namespace: "platform", Key: "smtp.password", ValueType: domain.ValueTypeString,
			Description: "SMTP 口令", Owner: "team-platform", Sensitive: true,
			SecretRule: &domain.SecretRule{
				MinLen: 16, MaxLen: 64, Encoding: "hex", AutoGenerate: true,
			},
		},
		domain.KeySpec{
			Namespace: "identity", Key: "auth.jwt.issuer", ValueType: domain.ValueTypeString,
			Required: true, Description: "JWT 签发者（iss）", Owner: "team-identity",
		},
		domain.KeySpec{
			Namespace: "identity", Key: "identity.sms.access_key_id", ValueType: domain.ValueTypeString,
			Description: "短信服务 AccessKeyId", Owner: "team-identity", Sensitive: true,
		},
	)
	if err != nil {
		t.Fatalf("register specs: %v", err)
	}
	return registry
}

func newSchemaServices(t *testing.T) (*app.InstallService, *memory.Repositories, *fakeEmitter) {
	t.Helper()
	t.Setenv("CONFIG_INSTALL_SETUP_TOKEN", "test-setup-token-32chars-xxxxxx")
	repos := memory.New()
	registry := schemaSpecs(t)
	emitter := &fakeEmitter{}
	ids := newIDs()
	options := app.Options{Service: "config-api", Now: func() time.Time { return testNow }}
	bootstrap, err := app.NewBootstrapService(repos.Items, repos.Releases, repos.Pointers,
		registry, emitter, ids, options)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	install, err := app.NewInstallService(app.InstallServiceConfig{
		Install: repos.Install, Pointers: repos.Pointers, Bootstrap: bootstrap,
		Specs: registry, Emitter: emitter, IDs: ids,
		Options:  options,
		SetupTok: os.Getenv("CONFIG_INSTALL_SETUP_TOKEN"),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := install.EnsureState(context.Background(), "prod", domain.InstallPending); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return install, repos, emitter
}

func TestInstallSchemaFirstInstall(t *testing.T) {
	install, _, _ := newSchemaServices(t)
	schema, err := install.Schema(context.Background(), "prod")
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Mode != app.InstallModeFirstInstall {
		t.Fatalf("mode=%s", schema.Mode)
	}
	if schema.BlockingCount != 2 || len(schema.BlockingKeys) != 2 {
		t.Fatalf("blocking=%d keys=%+v", schema.BlockingCount, schema.BlockingKeys)
	}
	for _, gap := range schema.BlockingKeys {
		if gap.Reason != "no_active_release" {
			t.Fatalf("reason=%s key=%s", gap.Reason, gap.Key)
		}
		if gap.Label == "" {
			t.Fatalf("missing label for %s", gap.Key)
		}
	}
	// 命名空间按展示顺序排列：platform 在内置表里排在 identity 之前。
	if len(schema.Namespaces) != 2 || schema.Namespaces[0].Namespace != "platform" {
		t.Fatalf("namespaces=%+v", schema.Namespaces)
	}
	if schema.Namespaces[0].BlockingCount != 1 || schema.Namespaces[1].BlockingCount != 1 {
		t.Fatalf("namespace blocking=%+v", schema.Namespaces)
	}

	byKey := make(map[string]app.SchemaField, len(schema.Fields))
	for _, field := range schema.Fields {
		byKey[field.Namespace+"|"+field.Key] = field
	}
	baseURL := byKey["platform|http.base_url"]
	if !baseURL.Required || !baseURL.Blocking || baseURL.Label != "对外基地址" {
		t.Fatalf("base_url=%+v", baseURL)
	}
	if baseURL.Default.Source != app.DefaultSourceNone || baseURL.Default.Value != "" {
		t.Fatalf("base_url default=%+v", baseURL.Default)
	}
	// prod-like 环境里 db.driver 的默认值来自叠加层，而不是代码 KeySpec.Default。
	if driver := byKey["platform|db.driver"]; driver.Default.Source != app.DefaultSourceProdLike ||
		driver.Default.Value != "mysql" {
		t.Fatalf("db.driver default=%+v", driver.Default)
	}
	if level := byKey["platform|log.level"]; level.Widget != "select" || len(level.Enum) != 3 {
		t.Fatalf("log.level=%+v", level)
	}
	if timeout := byKey["platform|http.timeout"]; timeout.Widget != "duration" {
		t.Fatalf("http.timeout=%+v", timeout)
	}
	sensitive := byKey["platform|smtp.password"]
	if sensitive.Widget != "password" || sensitive.Secret == nil {
		t.Fatalf("smtp.password=%+v", sensitive)
	}
	if !sensitive.Secret.AutoGenerate || sensitive.Secret.MinLen != 16 {
		t.Fatalf("secret rule=%+v", sensitive.Secret)
	}
	if sensitive.Default.Source != app.DefaultSourceNone {
		t.Fatalf("敏感项不应有默认值：%+v", sensitive.Default)
	}
	// issuer 是必填键，但 prod-like 叠加层给了值：默认来源应为 prod_default 而非 none。
	if issuer := byKey["identity|auth.jwt.issuer"]; issuer.Default.Source != app.DefaultSourceProdLike ||
		issuer.Default.Value != "admin-frame" {
		t.Fatalf("auth.jwt.issuer default=%+v", issuer.Default)
	}
	smsField := byKey["identity|identity.sms.access_key_id"]
	if smsField.Group != "identity.sms" || smsField.GroupTitle == "" {
		t.Fatalf("sms group=%q title=%q", smsField.Group, smsField.GroupTitle)
	}
	// 未声明 SecretRule 的敏感键不渲染生成规则，前端应退化为「必填输入」。
	if smsField.Secret != nil {
		t.Fatalf("identity.sms.access_key_id 无 SecretRule：%+v", smsField.Secret)
	}
	if byKey["platform|http.base_url"].Order >= byKey["identity|auth.jwt.issuer"].Order {
		t.Fatalf("order: platform 应排在 identity 之前")
	}
}

func TestInstallSchemaBlockingReasonAfterSeed(t *testing.T) {
	install, repos, _ := newSchemaServices(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 播种只处理有默认值的键；必填键仍缺失，但此时已有生效发布。
	if _, err := install.Apply(ctx, app.ApplyInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := repos.Pointers.Get(ctx, "prod", "platform"); err != nil {
		t.Fatalf("expected pointer: %v", err)
	}
	schema, err := install.Schema(ctx, "prod")
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Mode != app.InstallModeFirstInstall {
		t.Fatalf("mode=%s", schema.Mode)
	}
	// identity 的必填键 auth.jwt.issuer 在 prod-like 叠加层有值，播种后即不再阻塞；
	// 只有 http.base_url 既无 KeySpec.Default 也无叠加层默认值，继续缺。
	if schema.BlockingCount != 1 || len(schema.BlockingKeys) != 1 {
		t.Fatalf("blocking=%d keys=%+v", schema.BlockingCount, schema.BlockingKeys)
	}
	gap := schema.BlockingKeys[0]
	if gap.Namespace != "platform" || gap.Key != "http.base_url" ||
		gap.Reason != "missing_in_snapshot" {
		t.Fatalf("gap=%+v", gap)
	}
}

func TestInstallSchemaRepairAfterReady(t *testing.T) {
	install, _, _ := newSchemaServices(t)
	ctx := context.Background()
	actor := app.Actor{SubjectID: "system:setup-token", ActorType: "system", Channel: "admin"}
	if _, err := install.Begin(ctx, app.BeginInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := install.Apply(ctx, app.ApplyInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := install.Complete(ctx, app.CompleteInput{Env: "prod", Owner: "owner-a", Actor: actor}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	schema, err := install.Schema(ctx, "prod")
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Mode != app.InstallModeRepair {
		t.Fatalf("mode=%s", schema.Mode)
	}
	if schema.Status != string(domain.InstallReady) {
		t.Fatalf("status=%s", schema.Status)
	}
	if len(schema.Fields) == 0 {
		t.Fatal("repair 模式仍应返回完整字段清单（由前端按 blocking_keys 收窄）")
	}
}

func TestInstallSchemaRejectsUnknownEnv(t *testing.T) {
	install, _, _ := newSchemaServices(t)
	if _, err := install.Schema(context.Background(), "nope"); err == nil {
		t.Fatal("expected bad request for unknown env")
	}
}

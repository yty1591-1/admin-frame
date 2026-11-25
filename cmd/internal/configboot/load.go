// Package configboot 在业务进程启动期从 config-api 拉取配置（ADR-0016）。
package configboot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/secretruntime"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// Result 是启动拉取结果。
type Result struct {
	Values map[string]string
	Client *configclient.Client
	Env    string
	// InstallPending 为 true 时表示配置中心尚未 ready；Values 来自代码默认值兜底。
	InstallPending bool
	HTTPClient     *http.Client
	ConfigAPIURL   string
}

// Load 拉取配置：远端成功写缓存；失败读缓存；都没有则返回错误。
//
// ADR-0017：若安装态非 ready，允许用 key spec 默认值完成进程监听（InstallPending=true）。
func Load(ctx context.Context) (*Result, error) {
	base := strings.TrimSpace(os.Getenv("CONFIG_API_URL"))
	identity := strings.TrimSpace(os.Getenv("CONFIG_SERVICE_IDENTITY"))
	env := strings.TrimSpace(os.Getenv("CONFIGCENTER_ENV"))
	if env == "" {
		env = "dev"
	}
	if base == "" || identity == "" {
		return nil, fmt.Errorf("configboot: CONFIG_API_URL and CONFIG_SERVICE_IDENTITY are required")
	}
	namespaces := namespacesFromEnv()
	httpClient, err := secretruntime.LoadClientHTTPFromEnvFor(env)
	if err != nil {
		return nil, err
	}

	statusCtx, statusCancel := context.WithTimeout(ctx, 2*time.Second)
	installStatus, statusErr := FetchInstallStatus(statusCtx, base, httpClient)
	statusCancel()
	if statusErr == nil && !IsInstallReady(installStatus.Status) {
		values, buildErr := BuildSpecDefaultValues()
		if buildErr != nil {
			return nil, fmt.Errorf("configboot: build defaults while install pending: %w", buildErr)
		}
		return &Result{
			Values: values, Env: env, InstallPending: true,
			HTTPClient: httpClient, ConfigAPIURL: base,
		}, nil
	}

	source, err := configclient.NewHTTPSource(configclient.HTTPSourceConfig{
		BaseURL: base, ServiceIdentity: identity, HTTPClient: httpClient,
	})
	if err != nil {
		return nil, err
	}
	notifier, err := configclient.NewLongPoller(configclient.LongPollerConfig{
		BaseURL: base, ServiceIdentity: identity, HTTPClient: httpClient,
	})
	if err != nil {
		return nil, err
	}
	cacheDir := strings.TrimSpace(os.Getenv("CONFIGCENTER_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = "var/configcenter-cache"
	}
	cache, err := configclient.NewDirCache(cacheDir)
	if err != nil {
		return nil, err
	}
	client, err := configclient.NewClient(configclient.ClientConfig{
		Source: source, Notifier: notifier, Cache: cache,
		Env: env, Namespaces: namespaces, PrimaryNamespace: spec.NamespacePlatform,
	})
	if err != nil {
		return nil, err
	}
	pullCtx, cancel := context.WithTimeout(ctx, time.Duration(len(client.Namespaces()))*5*time.Second)
	defer cancel()
	refreshErr := client.Refresh(pullCtx)
	var missing []string
	for _, namespace := range client.Namespaces() {
		if _, loaded := client.SnapshotFor(namespace); !loaded {
			missing = append(missing, namespace)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("configboot: namespaces unavailable from config-api and cache: %s: %v",
			strings.Join(missing, ", "), refreshErr)
	}
	if len(client.Values()) == 0 {
		return nil, fmt.Errorf("configboot: no config values from config-api or cache")
	}
	return &Result{
		Values: client.Values(), Client: client, Env: env,
		HTTPClient: httpClient, ConfigAPIURL: base,
	}, nil
}

// ProcessIdentity 是进程身份证；按规范不进配置中心，须由各 cmd 写死注入。
//
// Env 必须在此注入（来自 CONFIGCENTER_ENV / boot.Env），保证 LoadFromValues 的
// Normalize/Validate 看到真实环境（CC-R-01）；禁止 Load 成功后再赋值 Env。
type ProcessIdentity struct {
	Service string
	Channel string
	// Env 部署环境：local/dev/test/staging/prod；空则不覆盖（由调用方保证）。
	Env string
	// Addr 可选；非空时覆盖共享 platform 快照中的监听地址（多进程同 namespace）。
	Addr string
}

// WithProcessIdentity 把进程身份证合并进拉取结果；须在 Validate 前调用。
func WithProcessIdentity(values map[string]string, id ProcessIdentity) map[string]string {
	out := make(map[string]string, len(values)+5)
	for key, value := range values {
		out[key] = value
	}
	if env := strings.TrimSpace(id.Env); env != "" {
		out["env"] = env
	}
	if service := strings.TrimSpace(id.Service); service != "" {
		out["service"] = service
	}
	if channel := strings.TrimSpace(id.Channel); channel != "" {
		out["channel"] = channel
		if strings.TrimSpace(out["auth.expected_channel"]) == "" {
			out["auth.expected_channel"] = channel
		}
	}
	if addr := strings.TrimSpace(id.Addr); addr != "" {
		// 监听地址属部署身份：进程声明优先于快照（附录 B）。
		out["addr"] = addr
		out["server.addr"] = addr
	}
	return out
}

// SetIfEmpty 仅在快照未提供时写入进程默认值（CC-R-15）。
func SetIfEmpty(values map[string]string, key, value string) {
	if values == nil || strings.TrimSpace(key) == "" {
		return
	}
	if strings.TrimSpace(values[key]) == "" {
		values[key] = value
	}
}

// ApplyProdLikeProcessDefaults 在 prod/staging 为端别强制项补齐快照缺失值。
//
// 共享 namespace 不能播种 iam.mode=enforce（会炸掉 c/worker）；由 admin/biz
// 在此补齐。JWT 与 C 端短信登录渠道路径同理。
func ApplyProdLikeProcessDefaults(values map[string]string, env, channel string) {
	if values == nil || !platformconfig.IsProdLikeEnv(env) {
		return
	}
	SetIfEmpty(values, "auth.mode", "jwt")
	SetIfEmpty(values, "auth.jwt.alg", "HS256")
	SetIfEmpty(values, "auth.jwt.issuer", "admin-frame")
	SetIfEmpty(values, "auth.jwt.audience",
		`["admin","biz","c","worker","internal"]`)

	switch strings.TrimSpace(channel) {
	case "admin", "biz":
		// 与 identity.enabled 同理：共享快照是 KeySpec 跨端安全默认值
		// （approval=false / cache=memory），admin/biz 在 prod 必须强制覆盖，
		// 不能 SetIfEmpty（否则永远吃到快照默认值，Validate 失败）。
		values["iam.mode"] = "enforce"
		values["iam.approval.enabled"] = "true"
		values["iam.cache.driver"] = "redis"
		// prod-like 播种 identity.mfa.admin_required=true（A 端门禁）；
		// biz 进程必须关掉，否则 Validate 拒绝 channel=biz。
		if strings.TrimSpace(channel) == "biz" {
			values["identity.mfa.admin_required"] = "false"
		}
	case "c":
		// 共享快照默认 sms.enabled=false / driver=memory；C 端 prod 必须强制覆盖。
		values["identity.sms.enabled"] = "true"
		values["identity.sms.driver"] = "aliyun"
		SetIfEmpty(values, "identity.sms.template_id", "bootstrap-placeholder")
		SetIfEmpty(values, "identity.sms.sign_name", "bootstrap-placeholder")
		if strings.TrimSpace(values["identity.sms.daily_budget"]) == "" ||
			strings.TrimSpace(values["identity.sms.daily_budget"]) == "0" {
			values["identity.sms.daily_budget"] = "1000"
		}
		values["identity.mfa.admin_required"] = "false"
	case "worker":
		values["identity.mfa.admin_required"] = "false"
	}
}

func namespacesFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("CONFIGCENTER_NAMESPACES"))
	if raw == "" || raw == "all" || raw == "*" {
		registry, err := spec.Platform()
		if err != nil {
			return []string{spec.NamespacePlatform}
		}
		return registry.Namespaces()
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		ns := strings.TrimSpace(part)
		if ns == "" {
			continue
		}
		if _, ok := seen[ns]; ok {
			continue
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}
	if len(out) == 0 {
		return []string{spec.NamespacePlatform}
	}
	return out
}

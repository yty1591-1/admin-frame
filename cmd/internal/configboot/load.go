// Package configboot 在业务进程启动期从 config-api 拉取配置（ADR-0016）。
package configboot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// Result 是启动拉取结果。
type Result struct {
	Values map[string]string
	Client *configclient.Client
	Env    string
}

// Load 拉取配置：远端成功写缓存；失败读缓存；都没有则返回错误。
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
	source, err := configclient.NewHTTPSource(configclient.HTTPSourceConfig{
		BaseURL: base, ServiceIdentity: identity,
	})
	if err != nil {
		return nil, err
	}
	notifier, err := configclient.NewLongPoller(configclient.LongPollerConfig{
		BaseURL: base, ServiceIdentity: identity,
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
	pullCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Refresh(pullCtx); err != nil {
		// Refresh 在部分成功时仍可能返回错误；有任意 Values 即可继续。
		if len(client.Values()) == 0 {
			return nil, fmt.Errorf("configboot: pull config failed and cache empty: %w", err)
		}
	}
	if len(client.Values()) == 0 {
		return nil, fmt.Errorf("configboot: no config values from config-api or cache")
	}
	return &Result{Values: client.Values(), Client: client, Env: env}, nil
}

// ProcessIdentity 是进程身份证；按规范不进配置中心，须由各 cmd 写死注入。
type ProcessIdentity struct {
	Service string
	Channel string
	// Addr 可选；非空时覆盖共享 platform 快照中的监听地址（多进程同 namespace）。
	Addr string
}

// WithProcessIdentity 把进程身份证合并进拉取结果；须在 Validate 前调用。
func WithProcessIdentity(values map[string]string, id ProcessIdentity) map[string]string {
	out := make(map[string]string, len(values)+4)
	for key, value := range values {
		out[key] = value
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
		out["addr"] = addr
		out["server.addr"] = addr
	}
	return out
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

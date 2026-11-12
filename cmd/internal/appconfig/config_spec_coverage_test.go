package appconfig_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// keyAlias 映射 YAML 路径到配置中心键（仅命名空间前缀差异）。
var keyAlias = map[string]string{
	"addr":             "server.addr",
	"shutdown_timeout": "server.shutdown_timeout",
}

// excludedKeys 是明确不进入配置中心的平台键及原因（密钥、部署身份、
// 后端专属配置、当前值类型不支持等）。
//
//nolint:gosec // 仅为排除清单，不包含任何凭据值
var excludedKeys = map[string]string{
	"env":                       "部署身份",
	"service":                   "部署身份",
	"channel":                   "部署身份",
	"http.trusted_proxies":      "列表值，随网关部署",
	"auth.jwt.active_kid":       "JWT 密钥环选择",
	"auth.jwt.leeway":           "JWT 校验细节随密钥环",
	"auth.jwt.public_key_file":  "公钥文件路径随密钥环部署",
	"auth.jwt.public_keys_file": "公钥文件路径随密钥环部署",
	"db.dsn":                    "含数据库凭据",
	"redis.username":            "含缓存凭据",
	"redis.password":            "含缓存凭据",
	"redis.key_prefix":          "缓存域段",
	"tracing.enabled":           "可空开关，由 exporter 推断",
	"tracing.sample_ratio":      "浮点采样率，M1 值类型暂不支持",
	"id.node_id":                "部署身份",
}

// excludedPrefixes 是按前缀排除的平台键（密钥、云厂商后端与跨域参数）。
//
//nolint:gosec // 仅为排除清单，不包含任何凭据值
var excludedPrefixes = map[string]string{
	"secrets.":           "密钥来源配置",
	"cors.":              "前端来源契约，随网关部署",
	"storage.local.":     "本地路径与权限随节点部署",
	"storage.s3.":        "云厂商桶与凭据",
	"storage.oss.":       "云厂商桶与凭据",
	"storage.cos.":       "云厂商桶与凭据",
	"storage.obs.":       "云厂商桶与凭据",
	"storage.lifecycle.": "生命周期策略随桶配置",
	"storage.buckets":    "桶映射随云厂商部署",
	"auth.jwt.secret":    "JWT 密钥",
	"auth.jwt.keys":      "JWT 密钥环",
	"async.broker.url":   "消息中间件凭据",
}

// deferredPrefixes 是尚未由域 owner 完成声明的业务域配置。
var deferredPrefixes = []string{"identity.", "audit.", "tenant.", "notification."}

// TestPlatformKeyCoverage 保证平台段新增配置字段必须显式声明或登记排除，
// 防止配置中心 key spec 与代码默认值脱节（CC-F-41 遗漏率 0）。
func TestPlatformKeyCoverage(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	declared := map[string]struct{}{}
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			declared[keySpec.Key] = struct{}{}
		}
	}

	var paths []string
	walkYAMLPaths(reflect.TypeOf(appconfig.Config{}), "", &paths)
	if len(paths) == 0 {
		t.Fatal("no yaml paths discovered")
	}

	deferred := 0
	for _, path := range paths {
		if hasAnyPrefix(path, deferredPrefixes) || (strings.HasPrefix(path, "iam.") && path != "iam.mode") {
			deferred++
			continue
		}
		key := path
		if alias, ok := keyAlias[path]; ok {
			key = alias
		}
		if _, ok := declared[key]; ok {
			continue
		}
		if reason, ok := excludedKeys[path]; ok {
			if reason == "" {
				t.Errorf("excluded key %s must carry a reason", path)
			}
			continue
		}
		if _, ok := excludedPrefixes[path]; ok {
			continue
		}
		if matchedPrefix(path, excludedPrefixes) {
			continue
		}
		t.Errorf("config key %q is neither declared in spec.Platform nor excluded; "+
			"add a KeySpec or an explicit exclusion reason", path)
	}

	t.Logf("platform coverage: %d yaml paths, %d declared keys, %d deferred domain keys",
		len(paths), len(declared), deferred)
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			path := keySpec.Key
			for alias, key := range keyAlias {
				if key == keySpec.Key {
					path = alias
					break
				}
			}
			if !containsPath(paths, path) {
				t.Errorf("declared key %q has no matching yaml field %q", keySpec.Key, path)
			}
		}
	}
}

// walkYAMLPaths 递归收集结构体上的 yaml 叶子路径；切片/映射/指针标量按叶子处理。
func walkYAMLPaths(t reflect.Type, prefix string, out *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		if prefix != "" {
			*out = append(*out, prefix)
		}
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		inline := false
		for _, option := range parts[1:] {
			if option == "inline" {
				inline = true
			}
		}
		next := prefix
		if !inline && name != "" {
			if prefix == "" {
				next = name
			} else {
				next = prefix + "." + name
			}
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			walkYAMLPaths(fieldType, next, out)
			continue
		}
		if next != "" {
			*out = append(*out, next)
		}
	}
}

func hasAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func matchedPrefix(path string, prefixes map[string]string) bool {
	for prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

package appconfig_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// excludedKeys 是明确不进入配置中心的平台键及原因（密钥、部署身份、
// 后端专属配置、当前值类型不支持等）。
//
//nolint:gosec // 仅为排除清单，不包含任何凭据值
var excludedKeys = map[string]string{
	"env":                                  "部署身份",
	"service":                              "部署身份",
	"channel":                              "部署身份",
	"http.trusted_proxies":                 "列表值，随网关部署",
	"auth.jwt.active_kid":                  "JWT 密钥环选择",
	"auth.jwt.leeway":                      "JWT 校验细节随密钥环",
	"auth.jwt.public_key_file":             "公钥文件路径随密钥环部署",
	"auth.jwt.public_keys_file":            "公钥文件路径随密钥环部署",
	"db.dsn":                               "含数据库凭据",
	"redis.username":                       "含缓存凭据",
	"redis.password":                       "含缓存凭据",
	"redis.key_prefix":                     "缓存域段",
	"tracing.enabled":                      "可空开关，由 exporter 推断",
	"tracing.sample_ratio":                 "浮点采样率，M1 值类型暂不支持",
	"id.node_id":                           "部署身份",
	"identity.hash_key_id":                 "随摘要密钥派生",
	"identity.revocation.state_ttl":        "派生值：默认随 access_ttl 计算",
	"identity.saml.replay_ttl":             "派生值：assertion_ttl + clock_skew",
	"identity.saml.name_id_format":         "空值按 persistent 回退",
	"identity.password.min_length":         "0 表示运行期默认 12",
	"identity.password.max_length":         "0 表示运行期默认 128",
	"identity.password.require_complexity": "指针语义：nil 表示使用默认 true",
	"identity.password.max_concurrent":     "0 表示按 CPU 数推导",
	"identity.password.temporary_length":   "0 表示运行期默认 20",
	"identity.mfa.totp_digits":             "0 表示运行期默认 6",
	"identity.mfa.totp_step":               "0 表示运行期默认 30s",
	"identity.mfa.totp_window":             "0 表示运行期默认 1",
	"identity.mfa.recovery_code_count":     "0 表示运行期默认 10",
	"identity.mfa.step_up_reuse_grace":     "0 表示运行期默认 2m",
	"identity.scim.auto_provision":         "指针语义：nil 表示使用默认 true",
	"identity.scim.provider":               "目录提供方标识随部署",
	"identity.saml.entity_id":              "SP 部署身份",
	"identity.saml.acs_url":                "部署地址",
	"identity.saml.login_redirect_url":     "部署地址",
	"identity.saml.certificate_pem":        "SP 证书随部署与轮换",
	"identity.saml.certificate_next_pem":   "SP 证书随部署与轮换",
	"identity.refresh_rate_limit.ip_burst": "0 表示运行期默认 40",
	"identity.oauth.rate_limit.ip_burst":   "0 表示运行期默认 40",
	"identity.saml.rate_limit.ip_burst":    "0 表示运行期默认 40",
	"identity.m2m.rate_limit.ip_burst":     "0 表示运行期默认 40",
	"identity.scim.rate_limit.ip_burst":    "0 表示运行期默认 40",
	"audit.export.bucket":                  "导出桶随对象存储部署",
	"audit.retention.archive_prefix":       "归档前缀随桶部署",
}

// excludedPrefixes 是按前缀排除的平台键（密钥、云厂商后端与跨域参数）。
//
//nolint:gosec // 仅为排除清单，不包含任何凭据值
var excludedPrefixes = map[string]string{
	"secrets.":                           "密钥来源配置",
	"cors.":                              "前端来源契约，随网关部署",
	"storage.local.":                     "本地路径与权限随节点部署",
	"storage.s3.":                        "云厂商桶与凭据",
	"storage.oss.":                       "云厂商桶与凭据",
	"storage.cos.":                       "云厂商桶与凭据",
	"storage.obs.":                       "云厂商桶与凭据",
	"storage.lifecycle.":                 "生命周期策略随桶配置",
	"storage.buckets":                    "桶映射随云厂商部署",
	"auth.jwt.secret":                    "JWT 密钥",
	"auth.jwt.keys":                      "JWT 密钥环",
	"async.broker.url":                   "消息中间件凭据",
	"identity.oauth.providers":           "第三方 IdP 端点与凭据随部署",
	"identity.saml.providers":            "第三方 IdP 元数据随部署",
	"identity.mfa.keys":                  "MFA 密钥环",
	"identity.mfa.secret_key":            "MFA 主密钥",
	"identity.mfa.active_key_id":         "密钥环选择",
	"identity.subject.":                  "主体加密密钥环",
	"identity.previous_hash_keys":        "摘要历史密钥",
	"identity.hash_key":                  "PII / 短信码摘要主密钥",
	"identity.saml.private_key":          "SAML 私钥",
	"identity.password.tenant_overrides": "商户级密码策略列表",
	"identity.m2m.allowed_scopes":        "权限码白名单（列表值）",
	"identity.scim.group_role_map":       "SCIM 组到角色映射",
	"identity.mfa.channels":              "MFA 端别列表",
	"identity.mfa.factors":               "MFA 因子白名单",
	"iam.breakglass.allowed_subjects":    "破玻璃主体白名单（列表值）",
	"notification.channels.email.smtp.":  "SMTP 连接与认证随基础设施部署",
	"notification.channels.sms.aliyun.":  "阿里云 AK 与端点随密钥托管部署",
	"notification.channels.push.apns.":   "APNs 凭据与端点随密钥托管部署",
	"notification.channels.push.fcm.":    "FCM 凭据与端点随密钥托管部署",
}

// excludedSuffixes 是按后缀排除的键（M1 暂不支持浮点值类型）。
var excludedSuffixes = []string{".rps", ".ip_rps"}

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

	reverseAliases := map[string]string{}
	for key, path := range spec.ValueAliases() {
		reverseAliases[path] = key
	}
	for _, path := range paths {
		if _, ok := declared[path]; ok {
			continue
		}
		if key, ok := reverseAliases[path]; ok {
			if _, declaredAlias := declared[key]; declaredAlias {
				continue
			}
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
		if matchedSuffix(path, excludedSuffixes) {
			continue
		}
		t.Errorf("config key %q is neither declared in spec.Platform nor excluded; "+
			"add a KeySpec or an explicit exclusion reason", path)
	}

	t.Logf("platform coverage: %d yaml paths, %d declared keys", len(paths), len(declared))
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			if !containsPath(paths, spec.YAMLPath(keySpec.Key)) {
				t.Errorf("declared key %q has no matching yaml field", keySpec.Key)
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

// matchedSuffix 判断路径是否命中按后缀排除的键。
func matchedSuffix(path string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(path, suffix) {
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

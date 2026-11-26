package appconfig_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// excludedKeys 是明确不进入配置中心的平台键及原因。
// 理由须对齐 docs/CONFIG_STANDARDS.md §3.1.1；禁止「M1 暂不支持」等过时表述（CC-R-18）。
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
	"db.dsn":                               "含数据库凭据；仅 ADMIN_FRAME_DB_DSN",
	"redis.key_prefix":                     "缓存域段；随节点部署",
	"tracing.enabled":                      "可空开关，由 exporter 推断",
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
	"identity.saml.entity_id":              "SP 部署身份",
	"identity.saml.acs_url":                "部署地址",
	"identity.saml.login_redirect_url":     "部署地址",
	"identity.saml.certificate_pem":        "SP 证书随部署与轮换",
	"identity.saml.certificate_next_pem":   "SP 证书随部署与轮换",
	"identity.saml.private_key_next":       "SAML 轮换私钥；随密钥子域 dual_version 待续",
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
	"cors.":                              "STANDARDS §3.1.1 CORS/本地路径；随网关部署",
	"storage.local.":                     "STANDARDS §3.1.1 CORS/本地路径；随节点部署",
	"storage.s3.session_token":           "STANDARDS §3.1.1 云厂商连接参数；临时会话凭据随 Provider",
	"storage.s3.disable_ssl":             "STANDARDS §3.1.1 云厂商连接参数；禁用 TLS 仅限开发，生产禁止",
	"storage.oss.":                       "STANDARDS §3.1.1 云厂商连接参数",
	"storage.cos.":                       "STANDARDS §3.1.1 云厂商连接参数",
	"storage.obs.":                       "STANDARDS §3.1.1 云厂商连接参数",
	"storage.lifecycle.":                 "STANDARDS §3.1.1 云厂商连接参数；生命周期随桶",
	"storage.buckets":                    "STANDARDS §3.1.1 云厂商连接参数；桶映射随部署",
	"notification.channels.email.smtp.password_env": "已废弃：密码走 notification.smtp.password 密钥子域",
	"notification.channels.sms.aliyun.":             "STANDARDS §3.1.1 云厂商连接参数；AK 走密钥子域",
	"notification.channels.push.apns.":              "STANDARDS §3.1.1 云厂商连接参数；凭据走密钥子域",
	"notification.channels.push.fcm.":               "STANDARDS §3.1.1 云厂商连接参数；凭据走密钥子域",
	"auth.jwt.secret":                               "JWT 密钥",
	"auth.jwt.keys":                                 "JWT 密钥环",
	"identity.oauth.providers":           "第三方 IdP 端点与凭据随部署",
	"identity.saml.providers":            "第三方 IdP 元数据随部署",
	"identity.mfa.keys":                  "STANDARDS §3.1.1 密钥环与列表",
	"identity.mfa.active_key_id":         "STANDARDS §3.1.1 密钥环与列表；环选择",
	"identity.subject.":                  "STANDARDS §3.1.1 密钥环与列表；active 走 subject.key",
	"identity.previous_hash_keys":        "STANDARDS §3.1.1 密钥环与列表",
	"identity.password.tenant_overrides": "商户级密码策略列表",
	"identity.mfa.channels":              "MFA 端别列表",
	"identity.mfa.factors":               "MFA 因子白名单",
	"iam.breakglass.allowed_subjects":    "破玻璃主体白名单（列表值）",
}

// excludedSuffixes 预留给确属永久部署侧、且无法用 string+pattern 登记的后缀。
// 浮点速率（*.rps / *.ip_rps）已按 CC-R-11 登记为 string+pattern，不得再排除。
var excludedSuffixes = []string{}

// TestExcludedReasonsForbidStalePhrases 守护排除理由不得再用「M1 暂不支持」等过时表述（CC-R-18）。
func TestExcludedReasonsForbidStalePhrases(t *testing.T) {
	forbidden := []string{
		"M1 暂不支持",
		"暂不支持 float",
		"值类型不支持",
		"当前值类型不支持",
	}
	check := func(kind, key, reason string) {
		t.Helper()
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s %q 缺少排除理由", kind, key)
			return
		}
		for _, phrase := range forbidden {
			if strings.Contains(reason, phrase) {
				t.Errorf("%s %q 理由 %q 含禁止表述 %q；须对齐 CONFIG_STANDARDS §3.1.1",
					kind, key, reason, phrase)
			}
		}
	}
	for key, reason := range excludedKeys {
		check("excludedKeys", key, reason)
	}
	for prefix, reason := range excludedPrefixes {
		check("excludedPrefixes", prefix, reason)
	}
	if len(excludedSuffixes) != 0 {
		t.Fatalf("excludedSuffixes 应为空（浮点已登记）；got %v", excludedSuffixes)
	}
}

// TestCORSStorageExclusionsCiteStandards 守护 cors/storage 排除必须引用 STANDARDS §3.1.1（CC-R-11）。
func TestCORSStorageExclusionsCiteStandards(t *testing.T) {
	required := []string{
		"cors.", "storage.local.", "storage.s3.session_token", "storage.s3.disable_ssl",
		"storage.oss.", "storage.cos.", "storage.obs.", "storage.lifecycle.", "storage.buckets",
	}
	for _, prefix := range required {
		reason, ok := excludedPrefixes[prefix]
		if !ok {
			t.Fatalf("missing permanent exclusion %q", prefix)
		}
		if !strings.Contains(reason, "STANDARDS §3.1.1") {
			t.Fatalf("%q reason %q must cite STANDARDS §3.1.1", prefix, reason)
		}
	}
}

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
			// M2/M3：密钥子域只进 config_secret，不要求 yaml 字段镜像。
			if keySpec.Sensitive && keySpec.SecretRule != nil {
				continue
			}
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

package spec

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// 默认值来源（与 OpenAPI SchemaDefault.source / 前端徽标对齐）。
const (
	// DefaultSourceNone 表示无推荐默认值（敏感键、或声明与叠加层皆空）。
	DefaultSourceNone = "none"
	// DefaultSourceCode 表示取自 KeySpec.Default（代码声明，通常本地友好）。
	DefaultSourceCode = "code_default"
	// DefaultSourceProd 表示取自 staging/prod 环境推荐叠加层。
	DefaultSourceProd = "prod_default"
	// DefaultSourceDev 表示取自 local/dev/test 环境推荐叠加层。
	DefaultSourceDev = "dev_default"
)

// ResolveDefault 按环境解析「推荐默认值」与来源。
//
// 优先级（CONFIG_STANDARDS §4.1 / CFG-02）：
//
//	环境叠加层（本文件） > KeySpec.Default > 空
//
// 安装向导 schema、Bootstrap 播种、reset-defaults 自愈必须走同一解析，
// 避免「向导提示的默认值」与「实际播种值」分叉。敏感键永不返回明文默认值。
func ResolveDefault(env string, keySpec domain.KeySpec) (value, source string) {
	if keySpec.Sensitive {
		return "", DefaultSourceNone
	}
	if overlay, overlaySource := envOverlay(env, keySpec.Key); overlay != "" {
		return overlay, overlaySource
	}
	if keySpec.Default != "" {
		return keySpec.Default, DefaultSourceCode
	}
	return "", DefaultSourceNone
}

// EnvDefaults 返回指定环境下的完整叠加表（不含 KeySpec.Default）。
// 供测试与运维清单枚举；业务解析请用 ResolveDefault。
func EnvDefaults(env string) map[string]string {
	switch {
	case isProdLike(env):
		return copyStringMap(prodLikeDefaults)
	case isNonProd(env):
		return copyStringMap(nonProdDefaults)
	default:
		return map[string]string{}
	}
}

// EmptyRecommendReasons 返回「故意不给推荐默认值」的非敏感键及理由。
//
// 覆盖率门禁：这些键在无 KeySpec.Default 时允许 ResolveDefault 返回 none；
// 其余非敏感键在 local/dev/test 与 staging/prod 都必须能解析出非空推荐值。
func EmptyRecommendReasons() map[string]string {
	return copyStringMap(emptyRecommendReasons)
}

func envOverlay(env, key string) (value, source string) {
	switch {
	case isProdLike(env):
		if v := prodLikeDefaults[key]; v != "" {
			return v, DefaultSourceProd
		}
	case isNonProd(env):
		if v := nonProdDefaults[key]; v != "" {
			return v, DefaultSourceDev
		}
	}
	return "", ""
}

func isProdLike(env string) bool {
	return env == domain.EnvProd || env == domain.EnvStaging
}

func isNonProd(env string) bool {
	switch env {
	case domain.EnvLocal, domain.EnvDev, domain.EnvTest:
		return true
	default:
		return false
	}
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// prodLikeDefaults 是 staging/prod 的推荐播种叠加层（跨 channel 安全）。
//
// 对齐 CONFIG_STANDARDS §4.4：覆盖本地友好的 KeySpec.Default，给出可过
// Validate 的生产基线。不写入端别互斥项（见 overlayForbiddenKeys）。
var prodLikeDefaults = map[string]string{
	// 持久化与对象存储：禁止 memory。
	"db.driver":      "mysql",
	"storage.driver": "s3",

	// 异步：prod Validate 要求启用；驱动保持 db，避免未配 NATS 即种 nats。
	"async.enabled": "true",
	"async.driver":  "db",

	// 鉴权：禁止 demo；Required 的 issuer/audience 一并补齐。
	"auth.mode":         "jwt",
	"auth.jwt.alg":      "HS256",
	"auth.jwt.issuer":   "admin-frame",
	"auth.jwt.audience": `["admin","biz","c","worker","internal"]`,

	// 幂等：admin/biz prod 强制开启 + redis。
	"idempotency.enabled":  "true",
	"idempotency.required": "true",
	"idempotency.driver":   "redis",

	// 限流：默认仍关闭，但驱动预先对齐 redis，避免日后启用不慎用 memory。
	"rate_limit.driver": "redis",

	// A 端 MFA 门禁；biz/c/worker 由 ApplyProdLikeProcessDefaults 覆盖为 false。
	"identity.mfa.admin_required": "true",

	// 通知渠道：生产默认关闭，避免 memory 驱动误发；真开渠道由运维改驱动并填凭据。
	"notification.channels.email.driver": "disabled",
	"notification.channels.sms.driver":   "disabled",
	"notification.channels.push.driver":  "disabled",

	// 日志：生产保持 info（覆盖 non-prod 的 debug 语义，显式登记）。
	"log.level": "info",
}

// nonProdDefaults 是 local/dev/test 的推荐播种叠加层。
//
// 目标：向导徽标与一键安装给出「可跑通」基线；显式写入与 KeySpec.Default
// 同值的本地友好项，使 schema 来源标为 dev_default，避免误读成生产推荐。
// 不强制 redis.addr：本机未必起 Redis，由 Gaps / 连通性探测提示。
var nonProdDefaults = map[string]string{
	"db.driver":      "memory",
	"storage.driver": "memory",

	"async.enabled": "false",
	"async.driver":  "db",

	"auth.mode":         "demo",
	"auth.jwt.alg":      "HS256",
	"auth.jwt.issuer":   "admin-frame",
	"auth.jwt.audience": `["admin","biz","c","worker","internal"]`,

	"idempotency.enabled":  "false",
	"idempotency.required": "false",
	"idempotency.driver":   "memory",

	"rate_limit.enabled": "false",
	"rate_limit.driver":  "memory",

	"identity.mfa.admin_required": "false",

	"notification.channels.email.driver": "memory",
	"notification.channels.sms.driver":   "memory",
	"notification.channels.push.driver":  "memory",

	"log.level": "debug",
}

// emptyRecommendReasons 是故意不给推荐默认值的非敏感键（覆盖率白名单）。
//
// 分类：
//   - 部署/端别互斥：由进程 WithProcessIdentity / ApplyProdLikeProcessDefaults 补齐；
//   - 运维填写的连接参数：安装向导人工录入，叠加强制值易误种；
//   - 条件必填附属字段：仅在驱动切换后由 Missing*Refs 判定。
var emptyRecommendReasons = map[string]string{
	"auth.expected_channel": "跟随部署 channel，禁止共享快照写死端别",
	"iam.mode":              "端别互斥；admin/biz 进程补齐 enforce，c/worker 保持 off",

	"redis.addr":     "运维按环境填写；叠加强制地址易导致连通性失败",
	"redis.username": "可空表示匿名；由运维按 Redis ACL 填写",

	"storage.key_prefix":  "可选前缀，无统一推荐",
	"storage.s3.bucket":   "条件必填（driver=s3/minio），由 MissingStorageRefs 判定",
	"storage.s3.region":   "条件必填（driver=s3/minio），由 MissingStorageRefs 判定",
	"storage.s3.endpoint": "可空表示云厂商默认端点",
	"tracing.endpoint":    "条件必填（exporter=otlp），无统一推荐",

	"identity.sms.template_id":    "厂商模板，启用短信后由运维填写",
	"identity.sms.sign_name":      "短信签名，启用短信后由运维填写",
	"identity.sms.locale":         "可选语言提示",
	"identity.sms.default_region": "可空表示只接受 E.164",
	"identity.sms.quota_timezone": "可空表示 UTC",
	"identity.scim.provider":      "启用 SCIM 后由运维填写目录提供方标识",

	"notification.channels.email.from":           "email.driver=smtp 时必填，见 MissingInfraRefs",
	"notification.channels.email.from_name":      "可选显示名",
	"notification.channels.email.smtp.host":      "email.driver=smtp 时必填，见 MissingInfraRefs",
	"notification.channels.email.smtp.username":  "按 SMTP 认证需要填写",
	"notification.channels.email.smtp.helo_name": "可选 EHLO 名称",
	"notification.channels.sms.template_id":      "启用短信渠道后由运维填写",
	"notification.channels.sms.sign_name":        "启用短信渠道后由运维填写",
	"notification.channels.push.platform":        "可空时按驱动推导",
}

// overlayForbiddenKeys 禁止进入环境叠加层的键（共享快照安全）。
//
// 与 CONFIG_STANDARDS §4.4 / ApplyProdLikeProcessDefaults 对齐：这些键若写入
// 共享 namespace，会与 c/worker 或其它端别 Validate 冲突。
var overlayForbiddenKeys = map[string]string{
	"iam.mode":                  "端别互斥（admin/biz=enforce）",
	"identity.enabled":          "端别互斥（由各 cmd 决定是否启用登录）",
	"iam.approval.enabled":      "仅 admin/biz prod 进程强制",
	"iam.cache.driver":          "仅 admin/biz prod 进程强制 redis",
	"identity.sms.enabled":      "仅 c 端进程强制",
	"identity.sms.driver":       "仅 c 端进程强制 aliyun",
	"identity.sms.template_id":  "c 端占位由进程 SetIfEmpty",
	"identity.sms.sign_name":    "c 端占位由进程 SetIfEmpty",
	"identity.sms.daily_budget": "c 端进程补齐",
	"auth.expected_channel":     "跟随部署 channel",
}

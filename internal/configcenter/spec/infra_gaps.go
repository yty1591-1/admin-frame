package spec

import "strings"

// Gap reason 常量：安装态 / status / OpenAPI 共用。
const (
	GapReasonNoActiveRelease    = "no_active_release"
	GapReasonMissingInSnapshot  = "missing_in_snapshot"
	GapReasonConnectivityFailed = "connectivity_failed"
	GapReasonDeployEnvMissing   = "deploy_env_missing"
	GapReasonSecretMissing      = "secret_missing"
)

// RedisRequired 报告当前 values 是否启用了依赖 Redis 的能力。
//
// 与业务进程 Validate 口径对齐：幂等 / 限流 / IAM 缓存 / 撤销任一走 redis 时，
// redis.addr 必须出现在生效快照（不得仅靠部署 env 兜底）。
func RedisRequired(values map[string]string) bool {
	if values == nil {
		return false
	}
	for _, key := range []string{
		"idempotency.driver",
		"rate_limit.driver",
		"iam.cache.driver",
		"identity.revocation.driver",
	} {
		if strings.EqualFold(strings.TrimSpace(values[key]), "redis") {
			return true
		}
	}
	return false
}

// MissingInfraRefs 返回 Redis/NATS/SMTP 等条件必填仍缺值的非敏感键（namespace/key）。
//
// 敏感条件键（async.broker.url、notification.smtp.password、redis.password）
// 不进快照，由安装态 Gaps 经密钥仓储另判；本函数只看普通 values。
func MissingInfraRefs(values map[string]string) []string {
	if values == nil {
		return nil
	}
	refs := make([]string, 0, 4)
	if RedisRequired(values) && strings.TrimSpace(values["redis.addr"]) == "" {
		refs = append(refs, NamespacePlatform+"/redis.addr")
	}
	if strings.EqualFold(strings.TrimSpace(values["notification.channels.email.driver"]), "smtp") {
		if strings.TrimSpace(values["notification.channels.email.smtp.host"]) == "" {
			refs = append(refs, NamespaceNotification+"/notification.channels.email.smtp.host")
		}
		if strings.TrimSpace(values["notification.channels.email.from"]) == "" {
			refs = append(refs, NamespaceNotification+"/notification.channels.email.from")
		}
	}
	return refs
}

// InfraSecretRef 是条件必填的敏感键引用。
type InfraSecretRef struct {
	Namespace string
	Key       string
}

// RequiredInfraSecrets 返回在当前 values 下必须存在的敏感键（不含 redis.password：可无密）。
func RequiredInfraSecrets(values map[string]string) []InfraSecretRef {
	if values == nil {
		return nil
	}
	out := make([]InfraSecretRef, 0, 2)
	enabled := strings.EqualFold(strings.TrimSpace(values["async.enabled"]), "true")
	driver := strings.ToLower(strings.TrimSpace(values["async.driver"]))
	if enabled && driver == "nats" {
		out = append(out, InfraSecretRef{Namespace: NamespacePlatform, Key: "async.broker.url"})
	}
	if strings.EqualFold(strings.TrimSpace(values["notification.channels.email.driver"]), "smtp") {
		out = append(out, InfraSecretRef{Namespace: NamespaceNotification, Key: "notification.smtp.password"})
	}
	return out
}

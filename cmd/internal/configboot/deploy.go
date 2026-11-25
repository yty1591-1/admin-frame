package configboot

import (
	"os"
	"strings"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// configAPIProcessJWTSecret 仅供 config-api 自举过 Validate；非业务签发密钥。
const configAPIProcessJWTSecret = "config-api-process-jwt-rehearsal-secret-32b" //nolint:gosec // G101: 进程自举占位密钥，非业务签发凭据。

// ApplyConfigAPIBootstrapValues 为 config-api 自举补齐 prod-like Validate 必填项。
//
// config-api 不消费配置中心快照，只能靠代码默认值 + 本函数注入；业务 JWT / 存储
// 连接仍由播种与部署侧注入提供给其它进程。
func ApplyConfigAPIBootstrapValues(values map[string]string, env string) {
	if values == nil {
		return
	}
	SetIfEmpty(values, "auth.expected_channel", "internal")
	ApplyDeployNodeID(values)
	if !platformconfig.IsProdLikeEnv(env) {
		return
	}
	SetIfEmpty(values, "auth.mode", "jwt")
	SetIfEmpty(values, "auth.jwt.alg", "HS256")
	SetIfEmpty(values, "auth.jwt.issuer", "admin-frame")
	SetIfEmpty(values, "auth.jwt.audience", "internal")
	SetIfEmpty(values, "auth.jwt.secret", configAPIProcessJWTSecret)
	// 进程本地盘即可；业务快照仍按 prod-like overlay 播种 storage.driver=s3。
	SetIfEmpty(values, "storage.driver", "local")
	SetIfEmpty(values, "storage.local.root", "var/prod-storage")
	SetIfEmpty(values, "audit.store", "db")
	SetIfEmpty(values, "http.timeout", "30s")
}

// ApplyDeployNodeID 从 ADMIN_FRAME_ID_NODE_ID 注入 id.node_id（部署身份，不进配置中心）。
func ApplyDeployNodeID(values map[string]string) {
	if values == nil {
		return
	}
	if nodeID := strings.TrimSpace(os.Getenv("ADMIN_FRAME_ID_NODE_ID")); nodeID != "" {
		SetIfEmpty(values, "id.node_id", nodeID)
	}
}

// ApplyDeployStorage 保留为兼容回退：从部署侧 env 注入对象存储连接参数。
//
// 自配置中心接管（CONFIG_STANDARDS §3.1.1 修订）后，正式路径是快照提供
// storage.s3.*（安装向导 / 管理面审批），AK/SK 走 config_secret；
// storage.local.* 仍属节点部署（不进 KeySpec）。本函数只在快照缺这些键时兜底。
func ApplyDeployStorage(values map[string]string) {
	if values == nil {
		return
	}
	driver := strings.ToLower(strings.TrimSpace(values["storage.driver"]))
	if driver == "local" {
		if root := strings.TrimSpace(os.Getenv("ADMIN_FRAME_STORAGE_LOCAL_ROOT")); root != "" {
			SetIfEmpty(values, "storage.local.root", root)
		} else {
			SetIfEmpty(values, "storage.local.root", "var/prod-storage")
		}
		return
	}
	if driver != "s3" && driver != "minio" {
		return
	}
	setStorage := func(envKey, valueKey string) {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			SetIfEmpty(values, valueKey, v)
		}
	}
	setStorage("ADMIN_FRAME_STORAGE_S3_ENDPOINT", "storage.s3.endpoint")
	setStorage("ADMIN_FRAME_STORAGE_S3_BUCKET", "storage.s3.bucket")
	setStorage("ADMIN_FRAME_STORAGE_S3_REGION", "storage.s3.region")
	setStorage("ADMIN_FRAME_STORAGE_S3_ACCESS_KEY", "storage.s3.access_key")
	setStorage("ADMIN_FRAME_STORAGE_S3_SECRET_KEY", "storage.s3.secret_key")
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ADMIN_FRAME_STORAGE_S3_FORCE_PATH_STYLE")), "true") {
		SetIfEmpty(values, "storage.s3.force_path_style", "true")
	}
}

// ApplyDeployOverlays 合并 node_id 与 storage 部署侧注入；业务进程在 Validate 前调用。
//
// redis.addr / redis.db / redis.username 仅来自配置中心快照（安装向导 / 审批发布），
// 不再从 ADMIN_FRAME_REDIS_* 兜底。
func ApplyDeployOverlays(values map[string]string) {
	ApplyDeployNodeID(values)
	ApplyDeployStorage(values)
}

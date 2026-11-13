// Package snapshotconfig 读取并验签本地配置中心快照，供进程启动期覆盖静态配置。
//
// 只依赖 pkg/configclient，不引入配置中心管理面实现，避免所有命令都链接
// 仓储、管理与 HTTP 组件（M1-9）。
package snapshotconfig

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// 本地签名快照是 M1 冷启动取值来源：部署把上一次成功发布的快照挂载到
// 进程内，验签通过后覆盖静态配置；缺失或验签失败由调用方降级。
const (
	// FileEnv 指定单个本地签名快照文件；未设置表示不接入配置中心。
	FileEnv = "CONFIGCENTER_SNAPSHOT_FILE"
	// FilesEnv 指定多个本地签名快照文件（逗号分隔），用于多 namespace。
	//
	// 设置后优先于 FileEnv；每个文件是一份独立签名快照，namespace 取自
	// 快照自身，重复 namespace 视为部署错误。
	FilesEnv = "CONFIGCENTER_SNAPSHOT_FILES"
	// PublicKeysEnv 配置可信验签公钥（kid=base64,...）。
	PublicKeysEnv = "CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS"
	// NamespacesEnv 是接管开关：启用的 namespace 列表（逗号分隔）。
	//
	// 空值等价于 platform（保持 M1 前行为）；all / * 表示加载到的全部
	// namespace。开关按 namespace 灰度开启，变更需重启进程。
	NamespacesEnv = "CONFIGCENTER_ENABLED_NAMESPACES"
	// AllowMissingEnv 允许配置文件缺失（无文件启动演练）；仅在已加载可信快照时生效。
	AllowMissingEnv = "CONFIGCENTER_ALLOW_MISSING_CONFIG"
)

// Startup 是启动期已验签快照集合。
type Startup struct {
	// Values 是启用 namespace 的合并取值（yaml 路径 → 字符串）。
	Values map[string]string
	// ByNamespace 是启用 namespace 各自的取值，供双读逐 namespace 对比。
	ByNamespace map[string]map[string]string
	// Namespaces 是开关启用的 namespace 列表（显式列表可能包含暂无快照的项）。
	Namespaces []string
	// Env 是首个快照声明的环境；无快照时为空。
	Env string
}

// Load 读取并验签全部本地快照，按开关合并启用 namespace 的取值。
//
// 未配置任一文件环境变量时返回 (nil, nil)，表示不接入配置中心。配置了
// 文件但缺少可信公钥、文件损坏、重复 namespace 或验签失败返回错误；
// 调用方应记录告警并继续使用文件配置（降级）。
func Load(ctx context.Context) (*Startup, error) {
	paths := snapshotPaths()
	if len(paths) == 0 {
		return nil, nil
	}
	rawKeys := strings.TrimSpace(os.Getenv(PublicKeysEnv))
	if rawKeys == "" {
		return nil, fmt.Errorf("configcenter: %s is required when snapshot file is set", PublicKeysEnv)
	}
	keys, err := configclient.ParsePublicKeys(rawKeys)
	if err != nil {
		return nil, err
	}
	verifier, err := configclient.NewVerifier(keys)
	if err != nil {
		return nil, err
	}

	snapshots := make(map[string]configclient.Snapshot, len(paths))
	env := ""
	for _, path := range paths {
		store, storeErr := configclient.NewFileStore(path)
		if storeErr != nil {
			return nil, storeErr
		}
		snapshot, loadErr := configclient.LoadFromStore(ctx, store, verifier)
		if loadErr != nil {
			return nil, fmt.Errorf("configcenter: load snapshot %s: %w", path, loadErr)
		}
		if snapshot.Namespace == "" {
			return nil, fmt.Errorf("configcenter: snapshot %s has empty namespace", path)
		}
		if env == "" {
			env = snapshot.Env
		} else if snapshot.Env != env {
			return nil, fmt.Errorf("configcenter: snapshot %s env %q does not match %q",
				path, snapshot.Env, env)
		}
		if _, duplicate := snapshots[snapshot.Namespace]; duplicate {
			return nil, fmt.Errorf("configcenter: duplicate snapshot for namespace %q", snapshot.Namespace)
		}
		snapshots[snapshot.Namespace] = snapshot
	}

	enabled := enabledNamespaces(snapshots)
	startup := &Startup{
		Values:      make(map[string]string),
		ByNamespace: make(map[string]map[string]string, len(enabled)),
		Namespaces:  enabled,
	}
	for _, namespace := range enabled {
		snapshot, ok := snapshots[namespace]
		if !ok {
			continue
		}
		values := make(map[string]string, len(snapshot.Values))
		for key, value := range snapshot.Values {
			values[key] = value
			startup.Values[key] = value
		}
		startup.ByNamespace[namespace] = values
		if startup.Env == "" {
			startup.Env = snapshot.Env
		}
	}
	return startup, nil
}

// StartupValues 读取并验签本地快照，返回合并后的扁平取值。
//
// 兼容单 namespace 调用方；需要逐 namespace 信息时使用 Load。
func StartupValues(ctx context.Context) (map[string]string, error) {
	startup, err := Load(ctx)
	if err != nil || startup == nil {
		return nil, err
	}
	return startup.Values, nil
}

// EnabledNamespaces 返回接管开关启用的 namespace 列表。
//
// all / * 展开为代码声明的全部 namespace；空值只启用 platform，保持 M1
// 前的单 namespace 行为。显式列表去重保序。
func EnabledNamespaces() []string {
	raw := strings.TrimSpace(os.Getenv(NamespacesEnv))
	if raw == "" {
		return []string{spec.NamespacePlatform}
	}
	if raw == "all" || raw == "*" {
		registry, err := spec.Platform()
		if err != nil {
			return []string{spec.NamespacePlatform}
		}
		return registry.Namespaces()
	}
	seen := make(map[string]struct{})
	namespaces := make([]string, 0, 4)
	for _, entry := range strings.Split(raw, ",") {
		namespace := strings.TrimSpace(entry)
		if namespace == "" {
			continue
		}
		if _, ok := seen[namespace]; ok {
			continue
		}
		seen[namespace] = struct{}{}
		namespaces = append(namespaces, namespace)
	}
	if len(namespaces) == 0 {
		return []string{spec.NamespacePlatform}
	}
	return namespaces
}

// AllowMissingConfig 判断是否允许无文件启动。
//
// 必须同时满足：显式开启 AllowMissingEnv，且已加载非空签名快照。否则缺失
// 配置会被静默降级为代码默认值，掩盖部署错误。
func AllowMissingConfig(snapshotValues map[string]string) bool {
	if len(snapshotValues) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowMissingEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// snapshotPaths 返回配置的快照文件列表；FilesEnv 优先于 FileEnv。
func snapshotPaths() []string {
	if raw := strings.TrimSpace(os.Getenv(FilesEnv)); raw != "" {
		paths := make([]string, 0, 4)
		for _, entry := range strings.Split(raw, ",") {
			if path := strings.TrimSpace(entry); path != "" {
				paths = append(paths, path)
			}
		}
		return paths
	}
	if path := strings.TrimSpace(os.Getenv(FileEnv)); path != "" {
		return []string{path}
	}
	return nil
}

// enabledNamespaces 解析开关；all / * 展开为已加载快照的 namespace。
func enabledNamespaces(snapshots map[string]configclient.Snapshot) []string {
	raw := strings.TrimSpace(os.Getenv(NamespacesEnv))
	if raw == "all" || raw == "*" {
		namespaces := make([]string, 0, len(snapshots))
		for namespace := range snapshots {
			namespaces = append(namespaces, namespace)
		}
		sort.Strings(namespaces)
		return namespaces
	}
	return EnabledNamespaces()
}

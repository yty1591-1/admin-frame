package platform

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/id"
)

// statefulSetOrdinal 匹配 StatefulSet Pod 名的数字序号后缀（如 admin-api-3）。
var statefulSetOrdinal = regexp.MustCompile(`-(\d+)$`)

// NewIDGenerator 按配置构造雪花 ID 生成器，返回生成器与实际节点号。
//
// 节点号来源：显式 id.node_id，或 statefulset 模式下从 HOSTNAME 末尾序号派生。
// 非法配置返回错误而非 panic，由调用方终止启动（S-04）。
func NewIDGenerator(cfg IDConfig) (id.Generator, int64, error) {
	hostname := ""
	if strings.EqualFold(strings.TrimSpace(cfg.NodeIDSource), IDSourceStatefulSet) {
		name, err := os.Hostname()
		if err != nil {
			return nil, 0, fmt.Errorf("id: read hostname: %w", err)
		}
		hostname = name
	}
	nodeID, err := resolveNodeID(cfg, hostname)
	if err != nil {
		return nil, 0, err
	}
	generator, err := id.NewSnowflake(id.Options{NodeID: nodeID})
	if err != nil {
		return nil, 0, fmt.Errorf("id: %w", err)
	}
	return generator, nodeID, nil
}

// resolveNodeID 计算生效节点号；hostname 仅在 statefulset 来源下使用。
func resolveNodeID(cfg IDConfig, hostname string) (int64, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.NodeIDSource)) {
	case "", IDSourceConfig, IDSourceEnv:
		if cfg.NodeID == nil {
			// 非生产环境允许未显式配置；prod/staging 已在 Validate 阶段拒绝。
			return 0, nil
		}
		return *cfg.NodeID, nil
	case IDSourceStatefulSet:
		match := statefulSetOrdinal.FindStringSubmatch(hostname)
		if match == nil {
			return 0, fmt.Errorf("id: hostname %q has no statefulset ordinal suffix", hostname)
		}
		nodeID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || nodeID < 0 || nodeID > maxNodeID {
			return 0, fmt.Errorf("id: statefulset ordinal must be in [0, %d]", maxNodeID)
		}
		return nodeID, nil
	default:
		return 0, fmt.Errorf("id: unsupported node_id_source %q", cfg.NodeIDSource)
	}
}

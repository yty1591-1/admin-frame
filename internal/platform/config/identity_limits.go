package config

// ID 节点号来源（id.node_id_source）。
const (
	// IDSourceConfig 使用显式 id.node_id（环境变量覆盖等价）。
	IDSourceConfig = "config"
	// IDSourceEnv 保留来源语义，实际同样读取 id.node_id（pkg/config 已合并环境变量）。
	IDSourceEnv = "env"
	// IDSourceStatefulSet 从 HOSTNAME 末尾 ordinal 派生 NodeID。
	IDSourceStatefulSet = "statefulset"
)

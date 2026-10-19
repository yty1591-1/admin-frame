// Package id 提供分布式唯一 ID 生成原语（Snowflake 风格）。
//
// 设计边界：
//   - 不得依赖 internal/；节点号由调用方注入（多实例须保证 NodeID 唯一）
//   - 默认位布局：1 符号位 + 41 毫秒时间戳 + 10 节点 + 12 序列（与 Twitter Snowflake 对齐）
//   - 业务前缀（如 ord_）由应用层拼接，本包只产出数值 ID / 十进制字符串
//   - 不负责任何持久化或中心化发号；跨机房可拆高位为 datacenter（见 Options）
//
// 典型用法：
//
//	gen, err := id.NewSnowflake(id.Options{NodeID: 1})
//	n, err := gen.Next()
//	s, err := gen.NextString()
package id

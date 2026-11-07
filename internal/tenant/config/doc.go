// Package config 定义租户域配置：租户终止后的数据处置时间线与相关限额
// （docs/tenant-data-lifecycle.md T-23 / ENT-15）。
//
// 边界：本包只持配置类型、默认值与校验；租户状态机、配额与数据处置在
// internal/tenant/{domain,app}，装配由 cmd/internal/tenantruntime 完成。
package config

// Package config 定义审计域配置：落库与导出存储、查询分页、保留期与归档策略
// （docs/AUDIT_STANDARDS.md AUD-06 / AUD-07）。
//
// 边界：本包只持配置类型、默认值与校验；审计事件的产生与出口在
// internal/shared/audit（契约）与 internal/platform/audit（出口实现），
// 查询 / 导出 / 保留清理在 internal/audit。
package config

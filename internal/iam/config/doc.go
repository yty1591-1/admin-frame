// Package config 定义 IAM（RBAC 权限管理）运行参数：鉴权数据来源模式、快照缓存与
// 校验参数，默认 mode=off 以保持历史静态权限表行为。
//
// 边界：本包只持配置类型、默认值与校验；权限判定与快照加载分别在 pkg/rbac 与
// internal/iam/app，装配由 cmd/internal/appconfig 与 cmd/internal/iamruntime 完成。
package config

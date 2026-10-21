// Package iam 是 RBAC 权限管理（IAM）业务域。
//
// 本包只承载跨子包共享的稳定标识（权限码、端别），不放业务逻辑；
// 领域模型、应用服务、持久化分别位于 domain / app / infrastructure 子包。
// 依赖方向固定为 transport → app → domain，infrastructure 实现 domain 定义的端口。
package iam

package domain

// ConstraintKind 是 RBAC2 约束违规的类型。
type ConstraintKind string

const (
	// ConstraintKindSodConflict 同一主体的有效角色闭包违反互斥组并发阈值。
	ConstraintKindSodConflict ConstraintKind = "sod_conflict"
	// ConstraintKindCardinality 超出单角色最大绑定数。
	ConstraintKindCardinality ConstraintKind = "cardinality"
	// ConstraintKindPrerequisite 缺少前置角色。
	ConstraintKindPrerequisite ConstraintKind = "prerequisite"
	// ConstraintKindHierarchy RBAC1 改挂父角色后会形成的结构性互斥冲突。
	ConstraintKindHierarchy ConstraintKind = "hierarchy_conflict"
)

// ConstraintViolation 描述一次约束违规，供错误响应、审计与违规报表复用。
//
// 约束只能在写入期保证一致：配置组 / 前置关系时若已存在违规，
// 调用方应拒绝变更并把本结构回传给管理端，而不是让带病数据进入鉴权。
type ConstraintViolation struct {
	// Kind 违规类型。
	Kind ConstraintKind
	// SubjectID 违规主体；配置期检查可能为空。
	SubjectID string
	// GroupID / GroupCode 互斥组标识，仅 SoD 违规填写。
	GroupID   string
	GroupCode string
	// RoleIDs 涉及的角色 ID，按需排序。
	RoleIDs []string
	// Detail 面向运维的可读说明。
	Detail string
}

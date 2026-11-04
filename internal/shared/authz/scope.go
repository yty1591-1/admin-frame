package authz

import (
	"context"
	"errors"
	"strings"
)

// ErrScopeDenied 表示目标租户不在主体的数据范围内。
//
// 服务层应以 errors.Is 识别后翻译为 40300（Forbidden）——authz 保持
// 只依赖标准库，不引入 apperr。
var ErrScopeDenied = errors.New("authz: target outside data scope")

// ScopeWhere 是数据范围的解析产物：仓储据此拼查询条件，app 据此校验
// 写目标（rbac-iam.md §4.4 第 5 条——app + 仓储条件双重落实，禁止
// 查全量再内存过滤）。
//
// v1 执行粒度为租户：ScopeSelf 在租户维度不产生额外条件，创建人比对
// 由 app 层显式完成（快照里的 SubjectID 供其使用）。
type ScopeWhere struct {
	// Type 数据范围类型（ScopeAll / ScopeMerchant / ScopeSelf /
	// ScopeMerchantSet）。
	Type string
	// TenantIDs 允许的租户集合；ScopeMerchant 恒为单元素，
	// ScopeMerchantSet 为显式枚举，ScopeAll 为空。
	TenantIDs []string
	// SubjectID 主体标识；ScopeSelf 时 app 比对创建人用。
	SubjectID string
}

// Allows 判断目标租户是否在范围内。
//
// fail closed：空集合（含 Merchant 主体缺 MerchantID 的异常快照）一律拒绝。
func (w ScopeWhere) Allows(tenantID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return false
	}
	switch w.Type {
	case ScopeAll:
		return true
	case ScopeMerchant, ScopeMerchantSet:
		for _, id := range w.TenantIDs {
			if id == tenantID {
				return true
			}
		}
		return false
	case ScopeSelf:
		// 租户维度 self 与 merchant 同界（本人数据必属本商户）；
		// 主体级比对由 app 层完成。
		for _, id := range w.TenantIDs {
			if id == tenantID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// Clause 生成租户列的 SQL 谓词片段与参数；范围 All 时返回空片段
// （调用方直接跳过拼接）。参数占位为 `?`，与 database/sql 约定一致。
//
// 该方法只做字符串与切片拼装，不引入任何 SQL 驱动依赖。
func (w ScopeWhere) Clause(tenantColumn string) (predicate string, args []any) {
	if w.Type == ScopeAll || len(w.TenantIDs) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(w.TenantIDs)), ", ")
	args = make([]any, 0, len(w.TenantIDs))
	for _, id := range w.TenantIDs {
		args = append(args, id)
	}
	return tenantColumn + " IN (" + placeholders + ")", args
}

// Where 解析指定资源的数据范围；资源未配置时的回退规则（fail closed）：
//   - biz 主体：ScopeMerchant（快照 MerchantID）；MerchantID 为空时
//     产出空集合——Allows / Clause 均拒绝一切；
//   - admin 主体：ScopeAll（运营视图为全量，细粒度由权限点控制）。
func (s Snapshot) Where(resource string) ScopeWhere {
	if scope, ok := s.ScopeFor(resource); ok {
		return newScopeWhere(scope, s.SubjectID, s.MerchantID)
	}
	if s.Channel == "biz" {
		ids := []string(nil)
		if merchant := strings.TrimSpace(s.MerchantID); merchant != "" {
			ids = []string{merchant}
		}
		return ScopeWhere{Type: ScopeMerchant, TenantIDs: ids, SubjectID: s.SubjectID}
	}
	return ScopeWhere{Type: ScopeAll, SubjectID: s.SubjectID}
}

// EnforceMerchant 校验目标租户是否在主体对指定资源的数据范围内；
// 越界返回 ErrScopeDenied。
func (s Snapshot) EnforceMerchant(resource, tenantID string) error {
	if !s.Where(resource).Allows(tenantID) {
		return ErrScopeDenied
	}
	return nil
}

// EnforceFromContext 是业务 app 的一行式接入点：从请求 context 取
// 快照并校验目标租户。
//
// 快照缺失时放行——部署形态可能未启用 iam（单进程最小形态，权限
// 已由 RequirePermission 中间件层挡住）；快照存在则必须通过。
func EnforceFromContext(ctx context.Context, resource, tenantID string) error {
	snapshot, ok := FromContext(ctx)
	if !ok {
		return nil
	}
	return snapshot.EnforceMerchant(resource, tenantID)
}

// newScopeWhere 把契约 Scope 归一为执行产物。
func newScopeWhere(scope Scope, subjectID, merchantID string) ScopeWhere {
	out := ScopeWhere{Type: scope.Type, SubjectID: subjectID}
	switch scope.Type {
	case ScopeMerchant:
		// B 端 merchant 范围的租户集合以快照 MerchantID 为准；
		// 配置里冗余的枚举忽略（防止角色配置漂移扩权）。
		if merchant := strings.TrimSpace(merchantID); merchant != "" {
			out.TenantIDs = []string{merchant}
		}
	case ScopeMerchantSet:
		ids := make([]string, 0, len(scope.MerchantIDs))
		for _, id := range scope.MerchantIDs {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				ids = append(ids, trimmed)
			}
		}
		out.TenantIDs = ids
	case ScopeSelf:
		// self 的租户界即主体所属商户；无商户标识则空集合拒绝。
		if merchant := strings.TrimSpace(merchantID); merchant != "" {
			out.TenantIDs = []string{merchant}
		}
	default:
		// ScopeAll 或未知类型：未知类型按拒绝处理（空集合）。
		out.Type = scope.Type
	}
	return out
}

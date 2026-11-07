package app

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TenantStatusChecker 判定 B 端商户当前是否在服务期（ENT-09 T-08/T-10）。
//
// 由租户域实现（tenant/app.Guard 结构化满足本接口），identity 不 import 租户域。
// 语义与 tenant/app.GuardPort 一致：
//   - (true, nil)      → 放行；
//   - (false, nil)     → 明确拒绝（停用 / 终止 / 未登记）；
//   - (false, err!=nil) → 无法判定（仓储故障），调用方必须 503 失败关闭。
type TenantStatusChecker interface {
	Verify(ctx context.Context, merchantID string) (allow bool, err error)
}

// checkTenantForProvision 是账号开通（T-10）与登录（T-08）共用的租户校验。
//
// merchantID 为空（A/C 端）或未配置检查器时直接放行。
// 未来 B 端邀请流（T-14）建号必须经过这里——不存在 / 已终止租户不得产生账号，
// 否则会出现「能登录但所有请求 403」的孤儿账号，排障时两头都对不上。
func checkTenantForProvision(ctx context.Context, checker TenantStatusChecker, merchantID string) error {
	if merchantID == "" || checker == nil {
		return nil
	}
	allow, err := checker.Verify(ctx, merchantID)
	if err != nil {
		return apperr.Unavailable("tenant status unavailable")
	}
	if !allow {
		return apperr.Forbidden("tenant is not available")
	}
	return nil
}

// MembershipChecker 判定主体是否为某租户的在册成员（ENT-09 T-22）。
//
// 由租户域实现（tenant_member 仓储），identity 不 import 租户域。
// 语义与 TenantStatusChecker 一致：err != nil 时调用方必须失败关闭——
// 「查不到成员关系」与「仓储故障」混为一谈会把切换变成越权通道。
type MembershipChecker interface {
	IsMember(ctx context.Context, tenantID, subjectID string) (bool, error)
}

// checkMembership 校验主体与目标租户的成员关系；检查器未配置时拒绝
// （fail-closed）：装配缺失不应静默放行，否则切换端点等于无鉴权换租。
func checkMembership(ctx context.Context, checker MembershipChecker, tenantID, subjectID string) error {
	if checker == nil {
		return apperr.Forbidden("tenant membership cannot be verified")
	}
	isMember, err := checker.IsMember(ctx, tenantID, subjectID)
	if err != nil {
		return apperr.Unavailable("tenant membership unavailable")
	}
	if !isMember {
		return apperr.Forbidden("subject is not a member of this tenant")
	}
	return nil
}

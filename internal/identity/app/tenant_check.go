package app

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TenantStatusChecker 判定 B 端商户当前是否在服务期（ENT-09 T-08/T-10）。
//
// 由租户域实现（tenant/app.Guard 结构化满足本接口），identity 不 import 租户域。
// 语义与 platform.TenantGuard 一致：
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

package app

import "context"

// GuardPort 判定某个商户当前是否可服务（ENT-09）。
//
// 端口归属租户域：平台层只接收满足该签名的函数作为鉴权链装配参数，
// 不再定义租户/配额语义的命名接口（PLAT-15 / ARCHITECTURE §5.6）。
// tenant/app.Guard 天生满足本端口。
type GuardPort interface {
	// Verify 返回是否放行。
	//
	// allow=false 且 err=nil 表示「明确拒绝」（租户停用 / 终止 / 未登记）；
	// err!=nil 表示「无法判定」（仓储故障），调用方必须按 503 失败关闭，
	// 不得降级为放行 —— 否则一次数据库抖动就等于全平台停用失效。
	Verify(ctx context.Context, merchantID string) (allow bool, err error)
}

// MembershipPort 判定主体是否为租户在册成员（ENT-09 T-22）。
type MembershipPort interface {
	IsMember(ctx context.Context, tenantID, subjectID string) (bool, error)
}

// 编译期断言：守卫实现始终满足端口定义。
var _ GuardPort = (*Guard)(nil)

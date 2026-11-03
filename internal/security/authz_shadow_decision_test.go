package security_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// decisionObserver 记录鉴权决策（allow / deny / unavailable）。
type decisionObserver struct {
	results []string
}

func (o *decisionObserver) OnDecision(_ context.Context, _, result string) {
	o.results = append(o.results, result)
}

// TestAuthorizerShadowRecordsDecision 验证 shadow 期也记录 IAM 侧判定。
//
// 背景（ENT-06 T6 实测）：shadow 模式此前只记录「与静态表的差异」，
// 不记录 IAM 的 allow / deny 分布，导致 iam_authz_decisions_total 恒为空——
// 看板「鉴权决策」面板在切 enforce 前（正是最需要它的时候）没有任何数据。
func TestAuthorizerShadowRecordsDecision(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1"), nil // IAM 侧无权限 → deny
	}}
	observer := &decisionObserver{}
	authorizer := security.NewAuthorizer(static,
		security.WithSnapshotLoader(loader),
		security.WithMode(security.ModeShadow),
		security.WithShadowObserver(&shadowObserver{}),
		security.WithDecisionObserver(observer),
	)
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}

	rec := serveWith(t, principal, authorizer.Require("file:read"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("shadow must follow static allow, status=%d", rec.Code)
	}
	if len(observer.results) != 1 || observer.results[0] != security.DecisionDeny {
		t.Fatalf("decisions=%+v want [%s]", observer.results, security.DecisionDeny)
	}
}

// TestAuthorizerShadowRecordsAllow 验证 IAM 放行时记录 allow。
func TestAuthorizerShadowRecordsAllow(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:read"), nil // IAM 侧有权限 → allow
	}}
	observer := &decisionObserver{}
	authorizer := security.NewAuthorizer(static,
		security.WithSnapshotLoader(loader),
		security.WithMode(security.ModeShadow),
		security.WithShadowObserver(&shadowObserver{}),
		security.WithDecisionObserver(observer),
	)
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}

	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if len(observer.results) != 1 || observer.results[0] != security.DecisionAllow {
		t.Fatalf("decisions=%+v want [%s]", observer.results, security.DecisionAllow)
	}
}

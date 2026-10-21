package security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/rbac"
)

// fakeLoader 是可编程的 SnapshotLoader，记录调用次数。
type fakeLoader struct {
	calls  atomic.Int32
	load   func(ctx context.Context, p auth.Principal) (authz.Snapshot, error)
	onCall func()
}

// LoadSnapshot 实现 security.SnapshotLoader。
func (l *fakeLoader) LoadSnapshot(ctx context.Context, p auth.Principal) (authz.Snapshot, error) {
	l.calls.Add(1)
	if l.onCall != nil {
		l.onCall()
	}
	return l.load(ctx, p)
}

// withPrincipal 注入已认证主体。
func withPrincipal(p auth.Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
}

// serveWith 组合主体注入与中间件后执行请求，返回响应记录。
func serveWith(t *testing.T, principal auth.Principal, mw func(http.Handler) http.Handler, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	h := withPrincipal(principal)(mw(handler))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// envelopeOf 解析错误信封。
func envelopeOf(t *testing.T, rec *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", rec.Body.String(), err)
	}
	return env
}

// adminSnapshot 构造 A 端快照，权限可定制。
func adminSnapshot(subjectID string, permissions ...string) authz.Snapshot {
	return authz.Snapshot{
		Channel:     string(auth.ChannelAdmin),
		SubjectType: "staff",
		SubjectID:   subjectID,
		Roles:       []string{"iam_ops"},
		Permissions: permissions,
		Version:     1,
		GeneratedAt: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
}

// TestAuthorizerEnforceUsesSnapshot 验证 enforce 模式只信任快照，不信任令牌角色。
func TestAuthorizerEnforceUsesSnapshot(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:write"), nil
	}}
	authorizer := security.NewAuthorizer(static, security.WithSnapshotLoader(loader))
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}

	if rec := serveWith(t, principal, authorizer.Require("file:write"), nil); rec.Code != http.StatusOK {
		t.Fatalf("snapshot permission status=%d want 200", rec.Code)
	}
	// 令牌里的 ops 角色不得放行（SEC：不信任 JWT 权限）。
	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("token role must be ignored, status=%d want 403", rec.Code)
	}
	if calls := loader.calls.Load(); calls != 2 {
		t.Fatalf("loader calls=%d want 2", calls)
	}
}

// TestSnapshotLoadFailureClosed 验证 loader 失败一律 503 / 50300，且不进入 handler。
func TestSnapshotLoadFailureClosed(t *testing.T) {
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return authz.Snapshot{}, context.DeadlineExceeded
	}}
	authorizer := security.NewAuthorizer(rbac.New(), security.WithSnapshotLoader(loader))
	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	rec := serveWith(t, auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1"}, authorizer.Require("file:read"), handler)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	if env := envelopeOf(t, rec); env.Code != apperr.CodeServiceUnavailable {
		t.Fatalf("biz code=%d want %d", env.Code, apperr.CodeServiceUnavailable)
	}
	if handlerCalled {
		t.Fatal("handler must not run when snapshot load fails")
	}
}

// TestSnapshotLoad4xxPassthrough 验证主体身份错误不被误报成 503。
func TestSnapshotLoad4xxPassthrough(t *testing.T) {
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return authz.Snapshot{}, apperr.Unauthorized("iam: merchant is required")
	}}
	authorizer := security.NewAuthorizer(nil, security.WithSnapshotLoader(loader))
	rec := serveWith(t, auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1"}, authorizer.Require("order:read"), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

// TestAuthorizerWritesSnapshotToContext 验证同一请求只加载一次，下游可读快照。
func TestAuthorizerWritesSnapshotToContext(t *testing.T) {
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:read"), nil
	}}
	authorizer := security.NewAuthorizer(rbac.New(), security.WithSnapshotLoader(loader))
	var seen authz.Snapshot
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, ok := authz.FromContext(r.Context())
		if !ok {
			t.Error("snapshot missing from request context")
		}
		seen = snapshot
		w.WriteHeader(http.StatusOK)
	})
	nested := authorizer.Require("file:read")(authorizer.Require("file:read")(final))
	rec := serveWith(t, auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1"}, func(h http.Handler) http.Handler { return h }, nested)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if loader.calls.Load() != 1 {
		t.Fatalf("loader calls=%d want 1 (context reuse)", loader.calls.Load())
	}
	if seen.SubjectID != "u-1" || len(seen.Permissions) != 1 {
		t.Fatalf("snapshot=%+v", seen)
	}
}

// TestAuthorizerRejectsSnapshotSubjectMismatch 验证快照主体不一致时失败关闭。
func TestAuthorizerRejectsSnapshotSubjectMismatch(t *testing.T) {
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-2", "file:read"), nil
	}}
	authorizer := security.NewAuthorizer(rbac.New(), security.WithSnapshotLoader(loader))
	rec := serveWith(t, auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1"}, authorizer.Require("file:read"), nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

// shadowObserver 记录 shadow 差异与加载错误。
type shadowObserver struct {
	mismatches []security.Mismatch
	loadErrors int
}

func (o *shadowObserver) OnMismatch(_ context.Context, mismatch security.Mismatch) {
	o.mismatches = append(o.mismatches, mismatch)
}

func (o *shadowObserver) OnLoadError(context.Context, string, error) { o.loadErrors++ }

// TestAuthorizerShadowKeepsStaticDecision 验证 shadow 不影响请求，只记录差异。
func TestAuthorizerShadowKeepsStaticDecision(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1"), nil // IAM 无权限
	}}
	observer := &shadowObserver{}
	authorizer := security.NewAuthorizer(static,
		security.WithSnapshotLoader(loader),
		security.WithMode(security.ModeShadow),
		security.WithShadowObserver(observer),
	)
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}

	rec := serveWith(t, principal, authorizer.Require("file:read"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("shadow must follow static allow, status=%d", rec.Code)
	}
	if len(observer.mismatches) != 1 {
		t.Fatalf("mismatches=%+v want 1", observer.mismatches)
	}
	mismatch := observer.mismatches[0]
	if !mismatch.StaticAllow || mismatch.IAMAllow || mismatch.Permission != "file:read" || mismatch.Channel != "admin" {
		t.Fatalf("mismatch=%+v", mismatch)
	}

	// loader 失败时仍按静态表放行，并记录错误。
	loader.load = func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return authz.Snapshot{}, context.DeadlineExceeded
	}
	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusOK {
		t.Fatalf("shadow load error must not fail request, status=%d", rec.Code)
	}
	if observer.loadErrors != 1 {
		t.Fatalf("load errors=%d want 1", observer.loadErrors)
	}

	// 快照主体不一致时 shadow 也必须回退静态表，而不是 503。
	loader.load = func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-2", "file:read"), nil
	}
	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusOK {
		t.Fatalf("shadow subject mismatch must not fail request, status=%d", rec.Code)
	}
	if observer.loadErrors != 2 {
		t.Fatalf("load errors=%d want 2 after mismatch", observer.loadErrors)
	}

	// 静态拒绝时 shadow 仍拒绝。
	if rec := serveWith(t, principal, authorizer.Require("file:write"), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("shadow static deny status=%d want 403", rec.Code)
	}
}

// TestAuthorizerOffIgnoresLoader 验证 off 模式不产生任何快照 I/O。
func TestAuthorizerOffIgnoresLoader(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		t.Error("loader must not be called in off mode")
		return authz.Snapshot{}, context.Canceled
	}}
	authorizer := security.NewAuthorizer(static,
		security.WithSnapshotLoader(loader),
		security.WithMode(security.ModeOff),
	)
	rec := serveWith(t, auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}, authorizer.Require("file:read"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if loader.calls.Load() != 0 {
		t.Fatalf("loader calls=%d want 0", loader.calls.Load())
	}
}

// TestAuthorizerRequireAnyEnforce 验证动态模式的 RequireAny 语义。
func TestAuthorizerRequireAnyEnforce(t *testing.T) {
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:write"), nil
	}}
	authorizer := security.NewAuthorizer(rbac.New(), security.WithSnapshotLoader(loader))
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1"}
	anyMW := authorizer.RequireAny("file:read", "file:write")
	if rec := serveWith(t, principal, anyMW, nil); rec.Code != http.StatusOK {
		t.Fatalf("any granted status=%d want 200", rec.Code)
	}
	denied := authorizer.RequireAny("file:read", "order:read")
	if rec := serveWith(t, principal, denied, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("any denied status=%d want 403", rec.Code)
	}
}

// TestAuthorizerShadowMatchesStaticWhenEquivalent 验证等价配置下 shadow 差异为 0。
func TestAuthorizerShadowMatchesStaticWhenEquivalent(t *testing.T) {
	static := rbac.New()
	static.GrantRole("ops", "file:read", "file:write")
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:read", "file:write"), nil
	}}
	observer := &shadowObserver{}
	authorizer := security.NewAuthorizer(static,
		security.WithSnapshotLoader(loader),
		security.WithMode(security.ModeShadow),
		security.WithShadowObserver(observer),
	)
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1", Roles: []string{"ops"}}
	for _, permission := range []string{"file:read", "file:write"} {
		if rec := serveWith(t, principal, authorizer.Require(permission), nil); rec.Code != http.StatusOK {
			t.Fatalf("permission %s status=%d want 200", permission, rec.Code)
		}
	}
	if len(observer.mismatches) != 0 {
		t.Fatalf("equivalent config must have no mismatch: %+v", observer.mismatches)
	}
	if observer.loadErrors != 0 {
		t.Fatalf("unexpected load errors: %d", observer.loadErrors)
	}
}

// decisionRecorder 记录动态鉴权决策结果。
type decisionRecorder struct {
	results []string
}

func (d *decisionRecorder) OnDecision(_ context.Context, _, result string) {
	d.results = append(d.results, result)
}

// TestAuthorizerDecisionObserver 验证 allow / deny / unavailable 三类决策事件。
func TestAuthorizerDecisionObserver(t *testing.T) {
	recorder := &decisionRecorder{}
	loader := &fakeLoader{load: func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return adminSnapshot("u-1", "file:read"), nil
	}}
	authorizer := security.NewAuthorizer(rbac.New(),
		security.WithSnapshotLoader(loader),
		security.WithDecisionObserver(recorder),
	)
	principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: "u-1"}
	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusOK {
		t.Fatalf("allow status=%d", rec.Code)
	}
	if rec := serveWith(t, principal, authorizer.Require("file:write"), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("deny status=%d", rec.Code)
	}
	loader.load = func(context.Context, auth.Principal) (authz.Snapshot, error) {
		return authz.Snapshot{}, context.DeadlineExceeded
	}
	if rec := serveWith(t, principal, authorizer.Require("file:read"), nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status=%d", rec.Code)
	}
	want := []string{security.DecisionAllow, security.DecisionDeny, security.DecisionUnavailable}
	if len(recorder.results) != len(want) {
		t.Fatalf("decisions=%v want %v", recorder.results, want)
	}
	for i := range want {
		if recorder.results[i] != want[i] {
			t.Fatalf("decisions=%v want %v", recorder.results, want)
		}
	}
}

// TestParseMode 验证模式解析白名单。
func TestParseMode(t *testing.T) {
	cases := []struct {
		raw     string
		want    security.Mode
		wantErr bool
	}{
		{raw: "", want: security.ModeOff},
		{raw: "off", want: security.ModeOff},
		{raw: "shadow", want: security.ModeShadow},
		{raw: "enforce", want: security.ModeEnforce},
		{raw: "ENFORCE", wantErr: true},
		{raw: "audit", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := security.ParseMode(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("mode %q must be rejected", tc.raw)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("mode=%v err=%v want %v", got, err, tc.want)
			}
		})
	}
}

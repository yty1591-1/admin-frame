package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/iam/transport/admin"
	"github.com/admin-frame/admin-frame/internal/iam/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// newBreakglassRouter 装配带破玻璃用例的 A 端路由，并播种系统角色。
func newBreakglassRouter(t *testing.T) *router {
	t.Helper()
	subject := new(string)
	*subject = "1001"
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	snapshots := persistence.NewMemorySnapshotRepository(roles, bindings)
	ids := &seqIDs{}
	roleSvc, err := app.NewRoleService(app.RoleServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.RoleLimits{MaxPermissionsPerRole: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingSvc, err := app.NewBindingService(app.BindingServiceConfig{
		Roles: roles, Bindings: bindings, Recorder: recorder{}, IDs: ids,
		Audit:  app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits: app.BindingLimits{MaxBindingsPerSubject: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := &domain.Role{
		ID: "9001", Channel: domain.ChannelAdmin, Scope: domain.RoleScopePlatform,
		Code: domain.BreakglassRoleCode, Name: "破玻璃超级权限",
		Type: domain.RoleTypeBreakglass, Status: domain.RoleStatusActive, Version: 1,
		CreatedAt: time.Now().UTC(), CreatedBy: "system", UpdatedBy: "system",
	}
	if err := roles.Save(context.Background(), seed, 0); err != nil {
		t.Fatal(err)
	}
	querySvc, err := app.NewQueryService(roles, bindings, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	applicator, err := app.NewRoleChangeApplicator(roleSvc, bindingSvc)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := app.NewChangeRequestService(app.ChangeRequestServiceConfig{
		Requests:   persistence.NewMemoryChangeRequestRepository(),
		Applicator: applicator,
		IDs:        ids,
		Limits:     app.ChangeLimits{RequireReason: true, AllowBreakglass: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	breakglass, err := app.NewBreakglassService(app.BreakglassServiceConfig{
		Changes:  changes,
		Recorder: recorder{},
		IDs:      ids,
		Audit:    app.AuditMeta{Service: "test", ActorType: "staff"},
		Limits:   app.BreakglassLimits{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Roles: roleSvc, Bindings: bindingSvc, Query: querySvc,
		Changes: changes, Breakglass: breakglass, Channel: domain.ChannelAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := auth.Principal{Channel: auth.ChannelAdmin, SubjectID: *subject}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
	perm := func(string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler { return next }
	}
	return &router{handler: admin.NewRouter(handler, authMW, perm), subject: subject}
}

func TestBreakglassHTTPFlow(t *testing.T) {
	rt := newBreakglassRouter(t)

	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/breakglass", map[string]any{
		"subject_type":     "staff",
		"subject_id":       "1001",
		"duration_seconds": 600,
		"reason":           "线上排障",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("request status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		SubjectID       string `json:"subject_id"`
		DurationSeconds int    `json:"duration_seconds"`
		Version         int64  `json:"version"`
	}
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "pending" || created.SubjectID != "1001" || created.DurationSeconds != 600 {
		t.Fatalf("created=%+v", created)
	}

	// 自审 40300。
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/breakglass/"+created.ID+"/approve",
		map[string]any{"version": created.Version}, nil)
	if rec.Code != http.StatusForbidden || env.Code != 40300 {
		t.Fatalf("self approve status=%d env=%+v", rec.Code, env)
	}

	// 换人审批并立即生效。
	rec, env = rt.as("1002").do(t, http.MethodPost, "/admin/v1/iam/breakglass/"+created.ID+"/approve",
		map[string]any{"version": created.Version}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Status    string     `json:"status"`
		BindingID string     `json:"binding_id"`
		ExpiresAt *time.Time `json:"expires_at"`
		Version   int64      `json:"version"`
	}
	if err := json.Unmarshal(env.Data, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Status != "applied" || applied.BindingID == "" || applied.ExpiresAt == nil {
		t.Fatalf("applied=%+v", applied)
	}
	if remaining := time.Until(*applied.ExpiresAt); remaining <= 0 || remaining > domain.MaxBreakglassDuration {
		t.Fatalf("expires in %s", remaining)
	}

	// 过期版本撤销必须 40900。
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/breakglass/"+created.ID+"/revoke",
		map[string]any{"version": applied.Version + 1}, nil)
	if rec.Code != http.StatusConflict || env.Code != 40900 {
		t.Fatalf("stale revoke status=%d env=%+v", rec.Code, env)
	}
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/breakglass/"+created.ID+"/revoke",
		map[string]any{"version": applied.Version}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBreakglassHTTPRejectsOverlongDuration(t *testing.T) {
	rt := newBreakglassRouter(t)
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/breakglass", map[string]any{
		"subject_type":     "staff",
		"subject_id":       "1001",
		"duration_seconds": int(domain.MaxBreakglassDuration/time.Second) + 1,
		"reason":           "超时长",
	}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("status=%d env=%+v", rec.Code, env)
	}
}

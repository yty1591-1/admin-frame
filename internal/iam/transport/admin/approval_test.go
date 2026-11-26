package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestApprovalRequiredRejectsDirectWrites 验证 iam.approval.enabled 开启后，
// 所有角色 / 绑定直写端点均返回 40900，且拦截发生在用例之前。
func TestApprovalRequiredRejectsDirectWrites(t *testing.T) {
	rt := newRouterWithApproval(t, true)

	cases := []struct {
		name   string
		method string
		path   string
		body   json.RawMessage
	}{
		{
			name:   "role_create",
			method: http.MethodPost,
			path:   "/admin/v1/iam/roles",
			body:   json.RawMessage(`{"channel":"admin","scope_type":"platform","code":"denied","name":"denied"}`),
		},
		{
			name:   "role_update",
			method: http.MethodPatch,
			path:   "/admin/v1/iam/roles/1",
			body:   json.RawMessage(`{"name":"x","version":1}`),
		},
		{
			name:   "role_permission_replace",
			method: http.MethodPut,
			path:   "/admin/v1/iam/roles/1/permissions",
			body:   json.RawMessage(`{"permissions":[],"version":1}`),
		},
		{
			name:   "role_clone",
			method: http.MethodPost,
			path:   "/admin/v1/iam/roles/1/clone",
			body:   json.RawMessage(`{"code":"copy","name":"copy"}`),
		},
		{
			name:   "role_delete",
			method: http.MethodDelete,
			path:   "/admin/v1/iam/roles/1",
		},
		{
			name:   "binding_create",
			method: http.MethodPost,
			path:   "/admin/v1/iam/bindings",
			body:   json.RawMessage(`{"subject_type":"user","subject_id":"u1","role_id":"1"}`),
		},
		{
			name:   "binding_delete",
			method: http.MethodDelete,
			path:   "/admin/v1/iam/bindings/1",
		},
		{
			name:   "role_template_create",
			method: http.MethodPost,
			path:   "/admin/v1/iam/role-templates",
			body:   json.RawMessage(`{"channel":"admin","code":"tpl","name":"tpl"}`),
		},
		{
			name:   "role_template_update",
			method: http.MethodPatch,
			path:   "/admin/v1/iam/role-templates/1",
			body:   json.RawMessage(`{"name":"x","version":1}`),
		},
		{
			name:   "role_template_permission_replace",
			method: http.MethodPut,
			path:   "/admin/v1/iam/role-templates/1/permissions",
			body:   json.RawMessage(`{"permissions":[],"version":1}`),
		},
		{
			name:   "role_template_delete",
			method: http.MethodDelete,
			path:   "/admin/v1/iam/role-templates/1",
			body:   json.RawMessage(`{"version":1}`),
		},
		{
			name:   "role_template_sync",
			method: http.MethodPost,
			path:   "/admin/v1/iam/role-templates/1/sync",
			body:   json.RawMessage(`{"reason":"sync"}`),
		},
		{
			name:   "role_template_instantiate",
			method: http.MethodPost,
			path:   "/admin/v1/iam/role-templates/1/instantiate",
			body:   json.RawMessage(`{"code":"reader","name":"reader"}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, env := rt.do(t, tc.method, tc.path, tc.body, nil)
			if rec.Code != http.StatusConflict || env.Code != apperr.CodeConflict {
				t.Fatalf("status=%d code=%d body=%s", rec.Code, env.Code, rec.Body.String())
			}
			if !strings.Contains(env.Message, tc.name) || !strings.Contains(env.Message, "/change-requests") {
				t.Fatalf("message=%q", env.Message)
			}
		})
	}

	// dry-run 只预览漂移，不改变授权，因此不受审批开关拦截。
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/role-templates/1/sync",
		json.RawMessage(`{"dry_run":true,"reason":"preview"}`), nil)
	if env.Code == apperr.CodeConflict {
		t.Fatalf("dry-run must not require approval: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 拦截在用例之前生效：没有任何角色被写入。
	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/roles", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 0 {
		t.Fatalf("roles total=%d, want 0", list.Total)
	}
}

// TestSystemAdminBypassesApprovalRequired 验证超级管理员在审批开关开启时仍可直写。
func TestSystemAdminBypassesApprovalRequired(t *testing.T) {
	rt := newRouterWithApproval(t, true).withRoles("system_admin")
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles",
		json.RawMessage(`{"channel":"admin","scope_type":"platform","code":"admin_direct","name":"admin_direct"}`), nil)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("system_admin direct write status=%d code=%d body=%s", rec.Code, env.Code, rec.Body.String())
	}
	if env.Code == apperr.CodeConflict {
		t.Fatalf("system_admin must not get approval conflict: %s", rec.Body.String())
	}
}

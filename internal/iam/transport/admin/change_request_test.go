package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// changePayload 是审批单响应的解码目标。
type changePayload struct {
	ID          string `json:"id"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	TargetID    string `json:"target_id"`
	Version     int64  `json:"version"`
	RequestedBy string `json:"requested_by"`
	ApprovedBy  string `json:"approved_by"`
}

// createRoleForChange 通过 HTTP 创建一个平台角色并返回 ID。
func createRoleForChange(t *testing.T, rt *router, code string) string {
	t.Helper()
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/roles", map[string]any{
		"scope_type":  "platform",
		"code":        code,
		"name":        "审批测试角色",
		"permissions": []string{"order:read"},
		"data_scopes": []map[string]any{{"resource": "order", "scope_type": "merchant"}},
	}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create role status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var role rolePayload
	if err := json.Unmarshal(env.Data, &role); err != nil {
		t.Fatal(err)
	}
	return role.ID
}

// TestChangeRequestHTTPFlow 覆盖 A 端审批单全链路：创建 → 自审被拒 → 他人审批 → 应用。
func TestChangeRequestHTTPFlow(t *testing.T) {
	rt := newRouter(t)
	roleID := createRoleForChange(t, rt, "ops_lead")

	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests", map[string]any{
		"channel":   "admin",
		"action":    "role_permission_replace",
		"target_id": roleID,
		"payload": map[string]any{
			"permissions": []string{"order:read", "order:write"},
			"data_scopes": []map[string]any{{"resource": "order", "scope_type": "merchant"}},
		},
		"reason": "补授写入权限",
	}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create change status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var created changePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "pending" || created.Version != 1 || created.RequestedBy != "1001" {
		t.Fatalf("created = %+v", created)
	}

	// 列表可见。
	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/change-requests?status=pending", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []changePayload `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	// total 必须与过滤条件一致：pending 有 1 张时 approved 必须为 0。
	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/change-requests?status=approved", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", rec.Code)
	}
	list = struct {
		Items []changePayload `json:"items"`
		Total int             `json:"total"`
	}{}
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("filtered list = %+v", list)
	}

	// 申请人自审必须 40300。
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version}, nil)
	if rec.Code != http.StatusForbidden || env.Code != 40300 {
		t.Fatalf("self approve status=%d env=%+v", rec.Code, env)
	}

	// 换审批人通过。
	rec, env = rt.as("1002").do(t, http.MethodPost,
		"/admin/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("approve status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var approved changePayload
	if err := json.Unmarshal(env.Data, &approved); err != nil {
		t.Fatal(err)
	}
	if approved.Status != "approved" || approved.ApprovedBy != "1002" || approved.Version != 2 {
		t.Fatalf("approved = %+v", approved)
	}

	// 应用：权限真正落库。
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests/"+created.ID+"/apply",
		map[string]any{"version": approved.Version}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("apply status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var applied changePayload
	if err := json.Unmarshal(env.Data, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Status != "applied" {
		t.Fatalf("applied = %+v", applied)
	}

	rec, env = rt.do(t, http.MethodGet, "/admin/v1/iam/roles/"+roleID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get role status=%d", rec.Code)
	}
	var role rolePayload
	if err := json.Unmarshal(env.Data, &role); err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, code := range role.Permissions {
		if code == "order:write" {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("role permissions = %v, want order:write", role.Permissions)
	}
}

func TestChangeRequestHTTPRejectsMistypedPayload(t *testing.T) {
	rt := newRouter(t)
	roleID := createRoleForChange(t, rt, "ops_readonly")

	// 载荷里的字段名拼错必须在应用时报 40000，避免静默变成「清空权限」。
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests", map[string]any{
		"channel":   "admin",
		"action":    "role_permission_replace",
		"target_id": roleID,
		"payload":   map[string]any{"permission": []string{"order:read"}},
		"reason":    "拼写错误",
	}, nil)
	if rec.Code != http.StatusOK || env.Code != 0 {
		t.Fatalf("create status=%d env=%+v body=%s", rec.Code, env, rec.Body.String())
	}
	var created changePayload
	if err := json.Unmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	rec, env = rt.as("1002").do(t, http.MethodPost,
		"/admin/v1/iam/change-requests/"+created.ID+"/approve",
		map[string]any{"version": created.Version}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d env=%+v", rec.Code, env)
	}
	rec, env = rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests/"+created.ID+"/apply",
		map[string]any{"version": 2}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("apply status=%d env=%+v", rec.Code, env)
	}
}

func TestChangeRequestHTTPRequiresVersion(t *testing.T) {
	rt := newRouter(t)
	rec, env := rt.do(t, http.MethodPost, "/admin/v1/iam/change-requests/1/approve",
		map[string]any{}, nil)
	if rec.Code != http.StatusBadRequest || env.Code != 40000 {
		t.Fatalf("missing version status=%d env=%+v", rec.Code, env)
	}
}

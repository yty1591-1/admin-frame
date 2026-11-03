package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/tenant/transport/rest"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// seqIDs 是测试用 ID 生成器。
type seqIDs struct {
	mu   sync.Mutex
	next int
}

func (g *seqIDs) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return int64(g.next), nil
}

func (g *seqIDs) NextString() (string, error) {
	id, err := g.Next()
	if err != nil {
		return "", err
	}
	return "t" + strconv.FormatInt(id, 10), nil
}

// quietEmitter 丢弃审计事件（审计语义在 app 层用例覆盖）。
type quietEmitter struct{}

func (quietEmitter) Emit(context.Context, audit.Event) error { return nil }

// newHandler 构造挂载内存仓储的 Handler。
func newHandler(t *testing.T) *rest.Handler {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	quotas, err := app.NewQuotaService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Service:    service,
		Members:    members,
		Quotas:     quotas,
		PageLimits: pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
		Channel:    "admin",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

// adminRequest 构造带 A 端 Principal 的请求。
func adminRequest(method, target, body string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	principal := auth.Principal{Channel: "admin", SubjectID: "u1"}
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

// envelope 是响应信封（只取需要的字段）。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// decodeEnvelope 解析响应信封。
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（body=%s）", err, recorder.Body.String())
	}
	return env
}

// tenantBody 是租户响应体。
type tenantBody struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	StatusReason string  `json:"status_reason"`
	Version      int64   `json:"version"`
	TerminatedAt *string `json:"terminated_at"`
}

// TestCreateReturns201Pending 验证创建返回 201 与 pending 状态。
func TestCreateReturns201Pending(t *testing.T) {
	handler := newHandler(t)
	recorder := httptest.NewRecorder()
	handler.Create(recorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Acme"}`))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", recorder.Code, recorder.Body.String())
	}
	env := decodeEnvelope(t, recorder)
	var tenant tenantBody
	if err := json.Unmarshal(env.Data, &tenant); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if tenant.Status != "pending" {
		t.Fatalf("status = %q, want pending", tenant.Status)
	}
	if tenant.Name != "Acme" {
		t.Fatalf("name = %q", tenant.Name)
	}
	if tenant.ID == "" {
		t.Fatal("id 不能为空")
	}
}

// TestCreateRejectsUnknownField 验证未知字段被拒绝（契约严格）。
func TestCreateRejectsUnknownField(t *testing.T) {
	handler := newHandler(t)
	recorder := httptest.NewRecorder()
	handler.Create(recorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Acme","status":"active"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（body=%s）", recorder.Code, recorder.Body.String())
	}
}

// TestCreateRequiresPrincipal 验证缺 Principal 返回 401。
func TestCreateRequiresPrincipal(t *testing.T) {
	handler := newHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/tenants", strings.NewReader(`{"name":"Acme"}`))
	handler.Create(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// TestSuspendRequiresReasonAndTransitions 验证停用必须带原因，带原因后状态变更。
func TestSuspendRequiresReasonAndTransitions(t *testing.T) {
	handler := newHandler(t)
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Beta"}`))
	var created tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, createRecorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}

	activate := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/activate", "")
	activate.SetPathValue("id", created.ID)
	activateRecorder := httptest.NewRecorder()
	handler.Activate(activateRecorder, activate)
	if activateRecorder.Code != http.StatusOK {
		t.Fatalf("activate status = %d（body=%s）", activateRecorder.Code, activateRecorder.Body.String())
	}

	noReason := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/suspend", `{}`)
	noReason.SetPathValue("id", created.ID)
	noReasonRecorder := httptest.NewRecorder()
	handler.Suspend(noReasonRecorder, noReason)
	if noReasonRecorder.Code != http.StatusBadRequest {
		t.Fatalf("无原因停用 status = %d, want 400", noReasonRecorder.Code)
	}

	suspend := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/suspend", `{"reason":"欠费"}`)
	suspend.SetPathValue("id", created.ID)
	suspendRecorder := httptest.NewRecorder()
	handler.Suspend(suspendRecorder, suspend)
	if suspendRecorder.Code != http.StatusOK {
		t.Fatalf("suspend status = %d（body=%s）", suspendRecorder.Code, suspendRecorder.Body.String())
	}
	var suspended tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, suspendRecorder).Data, &suspended); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if suspended.Status != "suspended" || suspended.StatusReason != "欠费" {
		t.Fatalf("status=%q reason=%q", suspended.Status, suspended.StatusReason)
	}
}

// TestGetMissingReturns404 验证不存在的租户返回 404 信封。
func TestGetMissingReturns404(t *testing.T) {
	handler := newHandler(t)
	request := adminRequest(http.MethodGet, "/admin/v1/tenants/absent", "")
	request.SetPathValue("id", "absent")
	recorder := httptest.NewRecorder()
	handler.Get(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", recorder.Code, recorder.Body.String())
	}
	if code := decodeEnvelope(t, recorder).Code; code != 40400 {
		t.Fatalf("业务码 = %d, want 40400", code)
	}
}

// TestListFiltersAndPages 验证列表过滤与分页字段。
func TestListFiltersAndPages(t *testing.T) {
	handler := newHandler(t)
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		recorder := httptest.NewRecorder()
		handler.Create(recorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"`+name+`"}`))
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d", name, recorder.Code)
		}
	}

	request := adminRequest(http.MethodGet, "/admin/v1/tenants?page=1&page_size=2&keyword=et", "")
	recorder := httptest.NewRecorder()
	handler.List(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d（body=%s）", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Items    []tenantBody `json:"items"`
		Total    int          `json:"total"`
		Page     int          `json:"page"`
		PageSize int          `json:"page_size"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &page); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Name != "Beta" {
		t.Fatalf("过滤结果 total=%d items=%d", page.Total, len(page.Items))
	}
	if page.Page != 1 || page.PageSize != 2 {
		t.Fatalf("分页回显 page=%d page_size=%d", page.Page, page.PageSize)
	}

	invalid := adminRequest(http.MethodGet, "/admin/v1/tenants?status=frozen", "")
	invalidRecorder := httptest.NewRecorder()
	handler.List(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态 status = %d, want 400", invalidRecorder.Code)
	}
}

// TestTerminateIsTerminal 验证终止后再次迁移返回 409。
func TestTerminateIsTerminal(t *testing.T) {
	handler := newHandler(t)
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Delta"}`))
	var created tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, createRecorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}

	terminate := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/terminate", `{"reason":"退租"}`)
	terminate.SetPathValue("id", created.ID)
	terminateRecorder := httptest.NewRecorder()
	handler.Terminate(terminateRecorder, terminate)
	if terminateRecorder.Code != http.StatusOK {
		t.Fatalf("terminate status = %d（body=%s）", terminateRecorder.Code, terminateRecorder.Body.String())
	}
	var terminated tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, terminateRecorder).Data, &terminated); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if terminated.TerminatedAt == nil {
		t.Fatal("终止必须返回 terminated_at")
	}

	resume := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/resume", "")
	resume.SetPathValue("id", created.ID)
	resumeRecorder := httptest.NewRecorder()
	handler.Resume(resumeRecorder, resume)
	if resumeRecorder.Code != http.StatusConflict {
		t.Fatalf("终态恢复 status = %d, want 409（body=%s）", resumeRecorder.Code, resumeRecorder.Body.String())
	}
}

// createTenant 创建一个 active 租户并返回其 ID。
func createTenant(t *testing.T, handler *rest.Handler) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.Create(recorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Acme"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d（body=%s）", recorder.Code, recorder.Body.String())
	}
	var created tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	activate := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/activate", "")
	activate.SetPathValue("id", created.ID)
	activateRecorder := httptest.NewRecorder()
	handler.Activate(activateRecorder, activate)
	if activateRecorder.Code != http.StatusOK {
		t.Fatalf("activate status = %d（body=%s）", activateRecorder.Code, activateRecorder.Body.String())
	}
	return created.ID
}

// TestMemberEndpoints 验证添加成员、列成员与改角色的 HTTP 契约。
func TestMemberEndpoints(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	add := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/members",
		`{"subject_id":"u1","role":"owner"}`)
	add.SetPathValue("id", tenantID)
	addRecorder := httptest.NewRecorder()
	handler.AddMember(addRecorder, add)
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("add member status = %d（body=%s）", addRecorder.Code, addRecorder.Body.String())
	}

	list := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/members", "")
	list.SetPathValue("id", tenantID)
	listRecorder := httptest.NewRecorder()
	handler.ListMembers(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list member status = %d", listRecorder.Code)
	}
	var page struct {
		Items []struct {
			SubjectID string `json:"subject_id"`
			Role      string `json:"role"`
			Status    string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, listRecorder).Data, &page); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if page.Total != 1 || page.Items[0].SubjectID != "u1" || page.Items[0].Role != "owner" {
		t.Fatalf("成员列表 = %+v", page)
	}

	// 唯一 owner 降级 → 409（领域规则经 HTTP 透出）。
	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u1", `{"role":"member"}`)
	patch.SetPathValue("id", tenantID)
	patch.SetPathValue("subject_id", "u1")
	patchRecorder := httptest.NewRecorder()
	handler.UpdateMember(patchRecorder, patch)
	if patchRecorder.Code != http.StatusConflict {
		t.Fatalf("降级唯一 owner status = %d, want 409（body=%s）", patchRecorder.Code, patchRecorder.Body.String())
	}

	// 缺少 role / status → 400。
	empty := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u1", `{}`)
	empty.SetPathValue("id", tenantID)
	empty.SetPathValue("subject_id", "u1")
	emptyRecorder := httptest.NewRecorder()
	handler.UpdateMember(emptyRecorder, empty)
	if emptyRecorder.Code != http.StatusBadRequest {
		t.Fatalf("空变更 status = %d, want 400", emptyRecorder.Code)
	}
}

// TestQuotaEndpoints 验证配额设置与查询。
func TestQuotaEndpoints(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	put := adminRequest(http.MethodPut, "/admin/v1/tenants/"+tenantID+"/quotas/user", `{"limit":50}`)
	put.SetPathValue("id", tenantID)
	put.SetPathValue("resource", "user")
	putRecorder := httptest.NewRecorder()
	handler.SetQuota(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("set quota status = %d（body=%s）", putRecorder.Code, putRecorder.Body.String())
	}

	get := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/quotas", "")
	get.SetPathValue("id", tenantID)
	getRecorder := httptest.NewRecorder()
	handler.ListQuotas(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("list quota status = %d", getRecorder.Code)
	}
	var page struct {
		Items []struct {
			Resource string `json:"resource"`
			Limit    int64  `json:"limit"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, getRecorder).Data, &page); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if page.Total != 1 || page.Items[0].Resource != "user" || page.Items[0].Limit != 50 {
		t.Fatalf("配额列表 = %+v", page)
	}

	// limit 缺失 → 400（用指针区分「未提供」与「设为 0」）。
	missing := adminRequest(http.MethodPut, "/admin/v1/tenants/"+tenantID+"/quotas/user", `{}`)
	missing.SetPathValue("id", tenantID)
	missing.SetPathValue("resource", "user")
	missingRecorder := httptest.NewRecorder()
	handler.SetQuota(missingRecorder, missing)
	if missingRecorder.Code != http.StatusBadRequest {
		t.Fatalf("缺 limit status = %d, want 400", missingRecorder.Code)
	}
}

// TestUpdateMemberIsAtomicOverHTTP 验证「非法状态 + 合法角色」不会产生半提交。
//
// 旧实现分两次调用用例：角色先落库，状态再报错，客户端看到 500 但权限已提升。
func TestUpdateMemberIsAtomicOverHTTP(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	add := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/members",
		`{"subject_id":"u1","role":"member"}`)
	add.SetPathValue("id", tenantID)
	addRecorder := httptest.NewRecorder()
	handler.AddMember(addRecorder, add)
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("add status = %d（body=%s）", addRecorder.Code, addRecorder.Body.String())
	}

	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u1",
		`{"role":"admin","status":"bogus"}`)
	patch.SetPathValue("id", tenantID)
	patch.SetPathValue("subject_id", "u1")
	patchRecorder := httptest.NewRecorder()
	handler.UpdateMember(patchRecorder, patch)
	if patchRecorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态 status = %d, want 400（body=%s）", patchRecorder.Code, patchRecorder.Body.String())
	}

	list := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/members", "")
	list.SetPathValue("id", tenantID)
	listRecorder := httptest.NewRecorder()
	handler.ListMembers(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRecorder.Code)
	}
	var page struct {
		Items []struct {
			Role string `json:"role"`
		} `json:"items"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, listRecorder).Data, &page); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Role != "member" {
		t.Fatalf("失败请求不应改角色，实际 = %+v", page.Items)
	}
}

// TestUpdateMemberAppliesBothFields 验证一次 PATCH 同时改角色与状态。
func TestUpdateMemberAppliesBothFields(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	add := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/members",
		`{"subject_id":"u1","role":"member"}`)
	add.SetPathValue("id", tenantID)
	addRecorder := httptest.NewRecorder()
	handler.AddMember(addRecorder, add)
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("add status = %d", addRecorder.Code)
	}

	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u1",
		`{"role":"admin","status":"disabled","reason":"转岗"}`)
	patch.SetPathValue("id", tenantID)
	patch.SetPathValue("subject_id", "u1")
	patchRecorder := httptest.NewRecorder()
	handler.UpdateMember(patchRecorder, patch)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d（body=%s）", patchRecorder.Code, patchRecorder.Body.String())
	}
	var updated struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, patchRecorder).Data, &updated); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if updated.Role != "admin" || updated.Status != "disabled" {
		t.Fatalf("变更后 = %+v", updated)
	}
}

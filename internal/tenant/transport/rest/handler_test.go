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
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
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
	seedTestPlan(t, repo)
	service, err := app.NewService(repo, &seqIDs{}, quietEmitter{}, app.Options{
		Service: "admin-api", Plans: repo, Members: repo, Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api", Quotas: repo})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	quotas, err := app.NewQuotaService(repo, repo, quietEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}
	invitations, err := app.NewInvitationService(repo, repo, repo, &seqIDs{}, quietEmitter{}, app.Options{
		Service: "admin-api", Quotas: repo,
	})
	if err != nil {
		t.Fatalf("NewInvitationService() error = %v", err)
	}
	handler, err := rest.NewHandler(rest.HandlerConfig{
		Service:     service,
		Members:     members,
		Quotas:      quotas,
		Invitations: invitations,
		PageLimits:  pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
		Channel:     "admin",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

// adminRequest 构造带 A 端 Principal 的请求（默认操作者 u1）。
func adminRequest(method, target, body string) *http.Request {
	return adminRequestAs("u1", method, target, body)
}

// adminRequestAs 指定操作者主体：审批用例需要「提交人 ≠ 审批人」。
func adminRequestAs(subject, method, target, body string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	principal := auth.Principal{Channel: "admin", SubjectID: subject}
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

// approveTenant 走 HTTP 端点补齐「提交 → 通过」审批（ENT-09 开通前置）。
func approveTenant(t *testing.T, handler *rest.Handler, tenantID string) {
	t.Helper()
	submit := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/approval", `{"note":"fixture"}`)
	submit.SetPathValue("id", tenantID)
	submitRecorder := httptest.NewRecorder()
	handler.SubmitApproval(submitRecorder, submit)
	if submitRecorder.Code != http.StatusOK {
		t.Fatalf("submit approval status = %d（body=%s）", submitRecorder.Code, submitRecorder.Body.String())
	}
	approve := adminRequestAs("u-approver", http.MethodPost, "/admin/v1/tenants/"+tenantID+"/approval/approve", `{"note":"ok"}`)
	approve.SetPathValue("id", tenantID)
	approveRecorder := httptest.NewRecorder()
	handler.ApproveApproval(approveRecorder, approve)
	if approveRecorder.Code != http.StatusOK {
		t.Fatalf("approve status = %d（body=%s）", approveRecorder.Code, approveRecorder.Body.String())
	}
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

	// T-12 开通前置：激活前先加一名 owner。
	addOwner := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/members",
		`{"subject_id":"u-owner","role":"owner"}`)
	addOwner.SetPathValue("id", created.ID)
	ownerRecorder := httptest.NewRecorder()
	handler.AddMember(ownerRecorder, addOwner)
	if ownerRecorder.Code != http.StatusCreated {
		t.Fatalf("add owner status = %d（body=%s）", ownerRecorder.Code, ownerRecorder.Body.String())
	}

	approveTenant(t, handler, created.ID)
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
	// T-12 开通前置：激活前先加一名 owner。
	addOwner := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/members",
		`{"subject_id":"u-owner","role":"owner"}`)
	addOwner.SetPathValue("id", created.ID)
	ownerRecorder := httptest.NewRecorder()
	handler.AddMember(ownerRecorder, addOwner)
	if ownerRecorder.Code != http.StatusCreated {
		t.Fatalf("add owner status = %d（body=%s）", ownerRecorder.Code, ownerRecorder.Body.String())
	}

	approveTenant(t, handler, created.ID)
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
	// createTenant 已种子 u-owner（owner）；u1 也是 owner → 列表 2 名，owner 排前。
	if page.Total != 2 || page.Items[0].SubjectID != "u-owner" || page.Items[1].SubjectID != "u1" {
		t.Fatalf("成员列表 = %+v", page)
	}

	// 降级 u1：u-owner 仍是 owner → 允许（200），证明「最后一名 owner」按数据判断而非按序。
	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u1", `{"role":"member"}`)
	patch.SetPathValue("id", tenantID)
	patch.SetPathValue("subject_id", "u1")
	patchRecorder := httptest.NewRecorder()
	handler.UpdateMember(patchRecorder, patch)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("降级非末位 owner status = %d, want 200（body=%s）", patchRecorder.Code, patchRecorder.Body.String())
	}
	// 降级末位 owner u-owner → 409（领域规则经 HTTP 透出）。
	lastOwner := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID+"/members/u-owner", `{"role":"member"}`)
	lastOwner.SetPathValue("id", tenantID)
	lastOwner.SetPathValue("subject_id", "u-owner")
	lastRecorder := httptest.NewRecorder()
	handler.UpdateMember(lastRecorder, lastOwner)
	if lastRecorder.Code != http.StatusConflict {
		t.Fatalf("降级末位 owner status = %d, want 409（body=%s）", lastRecorder.Code, lastRecorder.Body.String())
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
	// 套餐默认配额已在创建时物化（api.rps=100, user=50）；本用例 set user=50 覆盖同值。
	if page.Total != 2 ||
		page.Items[0].Resource != "api.rps" || page.Items[0].Limit != 100 ||
		page.Items[1].Resource != "user" || page.Items[1].Limit != 50 {
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
	// u-owner（createTenant 种子）与 u1 都不是 admin = 半提交未发生。
	if len(page.Items) != 2 {
		t.Fatalf("成员数 = %d, want 2", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Role == "admin" {
			t.Fatalf("失败请求不应改角色，实际含 admin: %+v", page.Items)
		}
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

// seedTestPlan 写入默认套餐（本包独立于 app_test 的助手）。
func seedTestPlan(t *testing.T, repo *persistence.MemoryRepository) {
	t.Helper()
	now := time.Now().UTC()
	plan := domain.Plan{
		ID: "plan-test", Code: "standard", Name: "标准套餐", IsDefault: true,
		Quotas: []domain.Quota{
			{Resource: "user", Limit: 50},
			{Resource: "api.rps", Limit: 100},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.UpsertPlan(plan); err != nil {
		t.Fatalf("UpsertPlan() error = %v", err)
	}
}

// TestPatchTenantProfile 验证 PATCH /tenants/{id} 的资料变更契约（T-11）。
func TestPatchTenantProfile(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID,
		`{"name":"Acme Renamed","contact_name":"张三","contact_email":"ops@acme.com"}`)
	patch.SetPathValue("id", tenantID)
	recorder := httptest.NewRecorder()
	handler.Update(recorder, patch)
	if recorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d（body=%s）", recorder.Code, recorder.Body.String())
	}
	var tenant tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &tenant); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if tenant.Name != "Acme Renamed" {
		t.Fatalf("name = %q", tenant.Name)
	}

	// GET 可见变更。
	get := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID, "")
	get.SetPathValue("id", tenantID)
	getRecorder := httptest.NewRecorder()
	handler.Get(getRecorder, get)
	var reloaded tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, getRecorder).Data, &reloaded); err != nil {
		t.Fatalf("get 解析失败: %v", err)
	}
	if reloaded.Name != "Acme Renamed" || reloaded.Version != tenant.Version {
		t.Fatalf("GET 与 PATCH 返回不一致: %+v vs %+v", reloaded, tenant)
	}

	// 空变更 → 400。
	empty := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID, `{}`)
	empty.SetPathValue("id", tenantID)
	emptyRecorder := httptest.NewRecorder()
	handler.Update(emptyRecorder, empty)
	if emptyRecorder.Code != http.StatusBadRequest {
		t.Fatalf("空变更 status = %d, want 400", emptyRecorder.Code)
	}

	// 不存在的租户 → 404。
	missing := adminRequest(http.MethodPatch, "/admin/v1/tenants/nope", `{"name":"X"}`)
	missing.SetPathValue("id", "nope")
	missingRecorder := httptest.NewRecorder()
	handler.Update(missingRecorder, missing)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", missingRecorder.Code)
	}
}

// TestSeatLimitReturns409WithDetail 验证席位超限的 HTTP 契约：409 + 当前值与上限（T-13）。
func TestSeatLimitReturns409WithDetail(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	// 需要配额用例（handler 内部成员用例已注入配额仓储）。
	put := adminRequest(http.MethodPut, "/admin/v1/tenants/"+tenantID+"/quotas/user", `{"limit":2}`)
	put.SetPathValue("id", tenantID)
	put.SetPathValue("resource", "user")
	putRecorder := httptest.NewRecorder()
	handler.SetQuota(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("set quota status = %d（body=%s）", putRecorder.Code, putRecorder.Body.String())
	}

	// createTenant 已种子 u-owner（1 席）；再加 1 席到上限，第 2 次添加应 409。
	for _, subject := range []string{"u1", "u2"} {
		add := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/members",
			`{"subject_id":"`+subject+`","role":"member"}`)
		add.SetPathValue("id", tenantID)
		recorder := httptest.NewRecorder()
		handler.AddMember(recorder, add)
		if subject == "u1" {
			if recorder.Code != http.StatusCreated {
				t.Fatalf("add u1 status = %d（body=%s）", recorder.Code, recorder.Body.String())
			}
			continue
		}
		if recorder.Code != http.StatusConflict {
			t.Fatalf("add u2 status = %d, want 409（body=%s）", recorder.Code, recorder.Body.String())
		}
		env := decodeEnvelope(t, recorder)
		if !strings.Contains(env.Message, "active 2") || !strings.Contains(env.Message, "limit 2") {
			t.Fatalf("409 应携带当前值与上限: %q", env.Message)
		}
	}
}

// TestInvitationEndpoints 验证邀请 5 个接口的 HTTP 契约（T-14）。
func TestInvitationEndpoints(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	// 创建：201 + 一次性明文 token。
	create := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations",
		`{"email":"new@acme.com","role":"member"}`)
	create.SetPathValue("id", tenantID)
	createRecorder := httptest.NewRecorder()
	handler.CreateInvitation(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d（body=%s）", createRecorder.Code, createRecorder.Body.String())
	}
	var created struct {
		ID         string  `json:"id"`
		Token      string  `json:"token"`
		Status     string  `json:"status"`
		TenantID   string  `json:"tenant_id"`
		InviteID   string  `json:"invitation_id"`
		AcceptedAt *string `json:"accepted_at"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, createRecorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if created.Token == "" || created.Status != "pending" {
		t.Fatalf("created = %+v", created)
	}

	// 接受：200 + 成员关系。
	accept := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/accept",
		`{"token":"`+created.Token+`","subject_id":"u-new"}`)
	accept.SetPathValue("id", tenantID)
	acceptRecorder := httptest.NewRecorder()
	handler.AcceptInvitation(acceptRecorder, accept)
	if acceptRecorder.Code != http.StatusOK {
		t.Fatalf("accept status = %d（body=%s）", acceptRecorder.Code, acceptRecorder.Body.String())
	}
	var accepted struct {
		Member struct {
			SubjectID string `json:"subject_id"`
			Status    string `json:"status"`
		} `json:"member"`
		Invitation struct {
			Status     string `json:"status"`
			AcceptedBy string `json:"accepted_by"`
		} `json:"invitation"`
		Idempotent bool `json:"idempotent"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, acceptRecorder).Data, &accepted); err != nil {
		t.Fatalf("accept 解析失败: %v", err)
	}
	if accepted.Member.SubjectID != "u-new" || accepted.Member.Status != "active" ||
		accepted.Invitation.Status != "accepted" || accepted.Idempotent {
		t.Fatalf("accepted = %+v", accepted)
	}

	// 重复接受：幂等 200。
	reAccept := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/accept",
		`{"token":"`+created.Token+`","subject_id":"u-new"}`)
	reAccept.SetPathValue("id", tenantID)
	reAcceptRecorder := httptest.NewRecorder()
	handler.AcceptInvitation(reAcceptRecorder, reAccept)
	if reAcceptRecorder.Code != http.StatusOK {
		t.Fatalf("re-accept status = %d（body=%s）", reAcceptRecorder.Code, reAcceptRecorder.Body.String())
	}
	if err := json.Unmarshal(decodeEnvelope(t, reAcceptRecorder).Data, &accepted); err != nil {
		t.Fatalf("re-accept 解析失败: %v", err)
	}
	if !accepted.Idempotent {
		t.Fatal("重复接受应标记 idempotent")
	}

	// 无效 token：400（不区分不存在与格式错误，防枚举）。
	badToken := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/accept",
		`{"token":"not-a-real-token","subject_id":"u-x"}`)
	badToken.SetPathValue("id", tenantID)
	badRecorder := httptest.NewRecorder()
	handler.AcceptInvitation(badRecorder, badToken)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("bad token status = %d, want 400", badRecorder.Code)
	}

	// 列表：包含已接受记录。
	list := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/invitations", "")
	list.SetPathValue("id", tenantID)
	listRecorder := httptest.NewRecorder()
	handler.ListInvitations(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRecorder.Code)
	}
	var invitations struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, listRecorder).Data, &invitations); err != nil {
		t.Fatalf("list 解析失败: %v", err)
	}
	if invitations.Total != 1 || invitations.Items[0].Status != "accepted" {
		t.Fatalf("invitations = %+v", invitations)
	}

	// 已接受的邀请不可 resend：409（终态不可逆）。
	resendAccepted := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/"+created.ID+"/resend", "")
	resendAccepted.SetPathValue("id", tenantID)
	resendAccepted.SetPathValue("invitation_id", created.ID)
	resendAcceptedRecorder := httptest.NewRecorder()
	handler.ResendInvitation(resendAcceptedRecorder, resendAccepted)
	if resendAcceptedRecorder.Code != http.StatusConflict {
		t.Fatalf("resend accepted status = %d, want 409", resendAcceptedRecorder.Code)
	}

	// 第二条邀请：重发（旧 token 失效）→ 撤销（新 token 也失效）。
	create2 := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations",
		`{"email":"bye@acme.com","role":"member"}`)
	create2.SetPathValue("id", tenantID)
	create2Recorder := httptest.NewRecorder()
	handler.CreateInvitation(create2Recorder, create2)
	var created2 struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, create2Recorder).Data, &created2); err != nil {
		t.Fatalf("create2 解析失败: %v", err)
	}

	resend := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/"+created2.ID+"/resend", "")
	resend.SetPathValue("id", tenantID)
	resend.SetPathValue("invitation_id", created2.ID)
	resendRecorder := httptest.NewRecorder()
	handler.ResendInvitation(resendRecorder, resend)
	if resendRecorder.Code != http.StatusOK {
		t.Fatalf("resend status = %d（body=%s）", resendRecorder.Code, resendRecorder.Body.String())
	}
	var resent struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, resendRecorder).Data, &resent); err != nil {
		t.Fatalf("resend 解析失败: %v", err)
	}
	if resent.Token == "" || resent.Token == created2.Token {
		t.Fatalf("resent token = %q", resent.Token)
	}
	staleAccept := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/accept",
		`{"token":"`+created2.Token+`","subject_id":"u-y"}`)
	staleAccept.SetPathValue("id", tenantID)
	staleAcceptRecorder := httptest.NewRecorder()
	handler.AcceptInvitation(staleAcceptRecorder, staleAccept)
	if staleAcceptRecorder.Code != http.StatusBadRequest {
		t.Fatalf("stale token accept status = %d, want 400", staleAcceptRecorder.Code)
	}

	revoke := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/"+created2.ID+"/revoke",
		`{"reason":"候选人放弃"}`)
	revoke.SetPathValue("id", tenantID)
	revoke.SetPathValue("invitation_id", created2.ID)
	revokeRecorder := httptest.NewRecorder()
	handler.RevokeInvitation(revokeRecorder, revoke)
	if revokeRecorder.Code != http.StatusOK {
		t.Fatalf("revoke status = %d（body=%s）", revokeRecorder.Code, revokeRecorder.Body.String())
	}
	revAccept := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/invitations/accept",
		`{"token":"`+resent.Token+`","subject_id":"u-y"}`)
	revAccept.SetPathValue("id", tenantID)
	revAcceptRecorder := httptest.NewRecorder()
	handler.AcceptInvitation(revAcceptRecorder, revAccept)
	if revAcceptRecorder.Code != http.StatusConflict {
		t.Fatalf("revoked accept status = %d, want 409", revAcceptRecorder.Code)
	}
}

// TestConditionalUpdateHeaders 验证 T-15：If-Match / expected_version / ETag 的 HTTP 契约。
func TestConditionalUpdateHeaders(t *testing.T) {
	handler := newHandler(t)
	tenantID := createTenant(t, handler)

	// GET 返回 ETag（createTenant 已 activate，当前版本 2）。
	get := adminRequest(http.MethodGet, "/admin/v1/tenants/"+tenantID, "")
	get.SetPathValue("id", tenantID)
	getRecorder := httptest.NewRecorder()
	handler.Get(getRecorder, get)
	etag := getRecorder.Header().Get("ETag")
	if etag != "2" {
		t.Fatalf("ETag = %q, want 2", etag)
	}

	// If-Match 过期版本 → 409 + 当前版本在消息里。
	suspend := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/suspend",
		`{"reason":"测试停用"}`)
	suspend.SetPathValue("id", tenantID)
	suspend.Header.Set("If-Match", `"99"`)
	suspendRecorder := httptest.NewRecorder()
	handler.Suspend(suspendRecorder, suspend)
	if suspendRecorder.Code != http.StatusConflict {
		t.Fatalf("If-Match 99 status = %d, want 409（body=%s）", suspendRecorder.Code, suspendRecorder.Body.String())
	}
	if !strings.Contains(suspendRecorder.Body.String(), "current 2") {
		t.Fatalf("409 应携带当前版本: %s", suspendRecorder.Body.String())
	}

	// If-Match * 不支持 → 400。
	wildcard := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/suspend",
		`{"reason":"测试停用"}`)
	wildcard.SetPathValue("id", tenantID)
	wildcard.Header.Set("If-Match", "*")
	wildcardRecorder := httptest.NewRecorder()
	handler.Suspend(wildcardRecorder, wildcard)
	if wildcardRecorder.Code != http.StatusBadRequest {
		t.Fatalf("If-Match * status = %d, want 400", wildcardRecorder.Code)
	}

	// If-Match 与 body.expected_version 不一致 → 400。
	conflict := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/suspend",
		`{"reason":"测试停用","expected_version":1}`)
	conflict.SetPathValue("id", tenantID)
	conflict.Header.Set("If-Match", "2")
	conflictRecorder := httptest.NewRecorder()
	handler.Suspend(conflictRecorder, conflict)
	if conflictRecorder.Code != http.StatusBadRequest {
		t.Fatalf("不一致 status = %d, want 400", conflictRecorder.Code)
	}

	// If-Match 正确版本 → 200 + 新 ETag。
	okSuspend := adminRequest(http.MethodPost, "/admin/v1/tenants/"+tenantID+"/suspend",
		`{"reason":"测试停用"}`)
	okSuspend.SetPathValue("id", tenantID)
	okSuspend.Header.Set("If-Match", "2")
	okRecorder := httptest.NewRecorder()
	handler.Suspend(okRecorder, okSuspend)
	if okRecorder.Code != http.StatusOK {
		t.Fatalf("正确 If-Match status = %d（body=%s）", okRecorder.Code, okRecorder.Body.String())
	}
	if okRecorder.Header().Get("ETag") != "3" {
		t.Fatalf("新 ETag = %q, want 3", okRecorder.Header().Get("ETag"))
	}

	// PATCH expected_version 过期 → 409；正确 → 200。
	patch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID,
		`{"name":"V2","expected_version":2}`)
	patch.SetPathValue("id", tenantID)
	patchRecorder := httptest.NewRecorder()
	handler.Update(patchRecorder, patch)
	if patchRecorder.Code != http.StatusConflict {
		t.Fatalf("PATCH 过期版本 status = %d, want 409（body=%s）", patchRecorder.Code, patchRecorder.Body.String())
	}
	okPatch := adminRequest(http.MethodPatch, "/admin/v1/tenants/"+tenantID,
		`{"name":"V2","expected_version":3}`)
	okPatch.SetPathValue("id", tenantID)
	okPatchRecorder := httptest.NewRecorder()
	handler.Update(okPatchRecorder, okPatch)
	if okPatchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH 正确版本 status = %d（body=%s）", okPatchRecorder.Code, okPatchRecorder.Body.String())
	}
}

// TestApprovalEndpoints 覆盖审批端点的 HTTP 契约：
// draft → submit → 自审被拒（409）→ 他人通过（200）→ 重复提交（409）。
func TestApprovalEndpoints(t *testing.T) {
	handler := newHandler(t)
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"Approval"}`))
	var created tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, createRecorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}

	// 1) 初始 draft。
	getRecorder := httptest.NewRecorder()
	get := adminRequest(http.MethodGet, "/admin/v1/tenants/"+created.ID+"/approval", "")
	get.SetPathValue("id", created.ID)
	handler.GetApproval(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get approval status = %d（body=%s）", getRecorder.Code, getRecorder.Body.String())
	}
	var draft struct {
		Status string          `json:"status"`
		Latest json.RawMessage `json:"latest"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, getRecorder).Data, &draft); err != nil {
		t.Fatalf("approval data 解析失败: %v", err)
	}
	if draft.Status != string(domain.ApprovalStatusDraft) || string(draft.Latest) != "null" {
		t.Fatalf("初始审批 = %s / %s, want draft / null", draft.Status, string(draft.Latest))
	}

	// 2) 提交。
	submitRecorder := httptest.NewRecorder()
	submit := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval", `{"note":"材料已备"}`)
	submit.SetPathValue("id", created.ID)
	handler.SubmitApproval(submitRecorder, submit)
	if submitRecorder.Code != http.StatusOK {
		t.Fatalf("submit status = %d（body=%s）", submitRecorder.Code, submitRecorder.Body.String())
	}

	// 3) 提交人自审 → 409。
	selfRecorder := httptest.NewRecorder()
	self := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval/approve", `{}`)
	self.SetPathValue("id", created.ID)
	handler.ApproveApproval(selfRecorder, self)
	if selfRecorder.Code != http.StatusConflict {
		t.Fatalf("自审 status = %d, want 409（body=%s）", selfRecorder.Code, selfRecorder.Body.String())
	}

	// 4) 他人通过 → 200。
	approveRecorder := httptest.NewRecorder()
	approve := adminRequestAs("u-approver", http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval/approve", `{}`)
	approve.SetPathValue("id", created.ID)
	handler.ApproveApproval(approveRecorder, approve)
	if approveRecorder.Code != http.StatusOK {
		t.Fatalf("approve status = %d（body=%s）", approveRecorder.Code, approveRecorder.Body.String())
	}

	// 5) 已通过再提交 → 409。
	againRecorder := httptest.NewRecorder()
	again := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval", `{}`)
	again.SetPathValue("id", created.ID)
	handler.SubmitApproval(againRecorder, again)
	if againRecorder.Code != http.StatusConflict {
		t.Fatalf("已通过再提交 status = %d, want 409（body=%s）", againRecorder.Code, againRecorder.Body.String())
	}
}

// TestApprovalSubmitAcceptsEmptyBody 锁定契约：提交 / 通过允许空请求体
// （requestBody.required=false），驳回必须带 body（原因必填由领域层校验）。
func TestApprovalSubmitAcceptsEmptyBody(t *testing.T) {
	handler := newHandler(t)
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, adminRequest(http.MethodPost, "/admin/v1/tenants", `{"name":"EmptyBody"}`))
	var created tenantBody
	if err := json.Unmarshal(decodeEnvelope(t, createRecorder).Data, &created); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}

	submitRecorder := httptest.NewRecorder()
	submit := adminRequest(http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval", "")
	submit.SetPathValue("id", created.ID)
	handler.SubmitApproval(submitRecorder, submit)
	if submitRecorder.Code != http.StatusOK {
		t.Fatalf("空 body 提交 status = %d（body=%s）", submitRecorder.Code, submitRecorder.Body.String())
	}

	rejectRecorder := httptest.NewRecorder()
	reject := adminRequestAs("u-approver", http.MethodPost, "/admin/v1/tenants/"+created.ID+"/approval/reject", "")
	reject.SetPathValue("id", created.ID)
	handler.RejectApproval(rejectRecorder, reject)
	if rejectRecorder.Code != http.StatusBadRequest {
		t.Fatalf("空 body 驳回 status = %d, want 400（body=%s）", rejectRecorder.Code, rejectRecorder.Body.String())
	}
}

package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/authz"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// principalTransport 场景请求：注入指定渠道的 Principal。
func principalRequest(t *testing.T, method, path, channel, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, http.NoBody)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	principal := auth.Principal{
		SubjectID:       "admin-1",
		Channel:         auth.Channel(channel),
		MerchantID:      "t-1",
		CurrentTenantID: "t-1",
	}
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	return req
}

// recordAppEmitter 记录审计事件（transport 测试专用最小实现）。
type recordAppEmitter struct{}

func (recordAppEmitter) Emit(_ context.Context, _ audit.Event) error { return nil }

// newTestIDs 构造测试 ID 生成器。
func newTestIDs(t *testing.T) id.Generator {
	t.Helper()
	gen, err := id.NewSnowflake(id.Options{NodeID: 7})
	if err != nil {
		t.Fatalf("NewSnowflake: %v", err)
	}
	return gen
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	requests := persistence.NewMemoryRequestRepository()
	deliveries := persistence.NewMemoryDeliveryRepository()
	templates := persistence.NewMemoryTemplateRepository()
	queries, err := app.NewQueryService(requests, deliveries)
	if err != nil {
		t.Fatalf("NewQueryService: %v", err)
	}
	tplSvc, err := app.NewTemplateService(templates, newTestIDs(t), &recordAppEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}
	handler, err := NewHandler(HandlerConfig{
		Queries:   queries,
		Templates: tplSvc,
		Channel:   "admin",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func TestNotificationHandlerListAndIsolation(t *testing.T) {
	// 种子两条通知（t-1 与 t-2）。
	requests := persistence.NewMemoryRequestRepository()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tid := range []string{"t-1", "t-2"} {
		n, err := domain.NewNotification("req-"+tid, tid, "tenant.invitation", "user-1",
			"notify:test:"+tid, []domain.Channel{domain.ChannelEmail}, "tenant.invitation",
			"zh-CN", domain.PriorityNormal, now.Add(time.Hour), now)
		if err != nil {
			t.Fatalf("NewNotification: %v", err)
		}
		if err := requests.Create(context.Background(), n); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	deliveries := persistence.NewMemoryDeliveryRepository()
	queries, _ := app.NewQueryService(requests, deliveries)
	templates := persistence.NewMemoryTemplateRepository()
	tplSvc, err := app.NewTemplateService(templates, newTestIDs(t), &recordAppEmitter{}, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}
	handler, err := NewHandler(HandlerConfig{Queries: queries, Templates: tplSvc, Channel: "admin"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// 默认查平台级（无结果）；显式指定 t-1 只看到自己的一条。
	req := principalRequest(t, "GET", "/admin/v1/notifications?tenant_id=t-1", "admin", "")
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Items []notificationResponse `json:"items"`
			Total int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 || resp.Data.Items[0].TenantID != "t-1" {
		t.Fatalf("isolation broken: %+v", resp.Data)
	}
	// 收件人明文不得出现。
	if strings.Contains(rec.Body.String(), "user-1@") {
		t.Fatal("recipient plaintext must not leak")
	}

	// 渠道不匹配必须 401。
	req = principalRequest(t, "GET", "/admin/v1/notifications?tenant_id=t-1", "biz", "")
	rec = httptest.NewRecorder()
	handler.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("channel mismatch must be 401, got %d", rec.Code)
	}
}

func TestNotificationHandlerUnauthenticated(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/notifications", http.NoBody)
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated must be 401, got %d", rec.Code)
	}
}

// TestNotificationHandlerDataScopeDenied ENT-12：快照存在时，目标租户
// 必须落在主体数据范围内；越界统一 40300（fail closed）。
func TestNotificationHandlerDataScopeDenied(t *testing.T) {
	handler := newTestHandler(t)

	newReq := func(tenant string) *http.Request {
		req := principalRequest(t, http.MethodGet, "/admin/v1/notifications?tenant_id="+tenant, "admin", "")
		snapshot := authz.Snapshot{
			Channel: "admin", SubjectID: "ops-1", MerchantID: "t-1",
			Scopes: map[string]authz.Scope{
				// 运营专员只管 t-2，t-1（自己商户）反而在范围外。
				"notification": {Type: authz.ScopeMerchantSet, MerchantIDs: []string{"t-2"}},
			},
		}
		return req.WithContext(authz.WithSnapshot(req.Context(), snapshot))
	}

	rec := httptest.NewRecorder()
	handler.List(rec, newReq("t-1"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope list must be 403, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.List(rec, newReq("t-2"))
	if rec.Code != http.StatusOK {
		t.Fatalf("in-scope list must be 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNotificationHandlerTemplateUpsertValidation(t *testing.T) {
	handler := newTestHandler(t)
	// 邮件模板缺主题必须 400。
	body := `{"key":"t.x","channel":"email","locale":"zh-CN","body":"b {{a}}"}`
	req := principalRequest(t, "POST", "/admin/v1/notification-templates", "admin", body)
	rec := httptest.NewRecorder()
	handler.UpsertTemplate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("subject required: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 合法 upsert 返回 201 + v1。
	body = `{"key":"t.x","channel":"email","locale":"zh-CN","subject":"s {{a}}","body":"b {{a}}"}`
	req = principalRequest(t, "POST", "/admin/v1/notification-templates", "admin", body)
	rec = httptest.NewRecorder()
	handler.UpsertTemplate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upsert status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data templateResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.Version != 1 {
		t.Fatalf("first version must be 1, got %d", resp.Data.Version)
	}
}

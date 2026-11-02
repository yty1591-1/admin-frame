package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	auditrest "github.com/admin-frame/admin-frame/internal/audit/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// stubReader 是审计只读仓储的测试替身。
type stubReader struct {
	events []audit.Event
}

// List 返回预置事件。
func (s *stubReader) List(_ context.Context, _ domain.Filter) ([]audit.Event, error) {
	return s.events, nil
}

// Count 返回预置总数。
func (s *stubReader) Count(_ context.Context, _ domain.Filter) (int, error) {
	return len(s.events), nil
}

// Stream 逐条回调预置事件。
func (s *stubReader) Stream(_ context.Context, _ domain.Filter, emit func(audit.Event) error) error {
	for _, event := range s.events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

// stubIDs 是自增 ID 生成器。
type stubIDs struct{ n int }

// Next 返回自增 ID。
func (s *stubIDs) Next() (int64, error) {
	s.n++
	return int64(s.n), nil
}

// NextString 返回自增 ID 字符串。
func (s *stubIDs) NextString() (string, error) {
	id, err := s.Next()
	if err != nil {
		return "", err
	}
	return time.Unix(id, 0).UTC().Format("20060102T150405Z"), nil
}

// newHandler 构造带测试替身的审计 Handler。
func newHandler(t *testing.T) *auditrest.Handler {
	t.Helper()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	reader := &stubReader{events: []audit.Event{{
		EventID:      "evt-1",
		Action:       audit.ActionAuthLogin,
		ActorID:      "admin-1",
		Channel:      "admin",
		ResourceType: "session",
		ResourceID:   "s-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   now,
		TraceID:      "trace-1",
		Service:      "admin-api",
	}}}
	svc, err := app.NewQueryService(reader, nil, &stubIDs{}, app.QueryOptions{
		MaxRange:       24 * time.Hour,
		MaxPageSize:    100,
		ExportMaxRange: 24 * time.Hour,
		ExportMaxRows:  10,
	}, "admin-api")
	if err != nil {
		t.Fatalf("NewQueryService() error = %v", err)
	}
	handler, err := auditrest.NewHandler(auditrest.HandlerConfig{
		Query:          svc,
		QueryLimits:    pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
		MaxRange:       24 * time.Hour,
		ExportMaxRange: 24 * time.Hour,
		Channel:        "admin",
		Service:        "admin-api",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

// adminRequest 构造带 A 端主体的请求。
func adminRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, http.NoBody)
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{
		Channel:   "admin",
		SubjectID: "admin-1",
	})
	return req.WithContext(ctx)
}

// TestListRequiresTimeWindow 验证缺时间窗的查询被拒绝，避免全表扫描。
func TestListRequiresTimeWindow(t *testing.T) {
	handler := newHandler(t)
	rec := httptest.NewRecorder()
	handler.List(rec, adminRequest(t, http.MethodGet, "/admin/v1/audit/events?page=1"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "from and to are required") {
		t.Fatalf("body = %s, want time window error", rec.Body.String())
	}
}

// TestListReturnsItemsAndTotal 验证查询响应结构与分页字段。
func TestListReturnsItemsAndTotal(t *testing.T) {
	handler := newHandler(t)
	target := "/admin/v1/audit/events?from=2026-11-02T00:00:00Z&to=2026-11-02T12:00:00Z&page=1&page_size=10"
	rec := httptest.NewRecorder()
	handler.List(rec, adminRequest(t, http.MethodGet, target))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data struct {
			Items []auditrest.EventDTO `json:"items"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rec.Body.String())
	}
	if payload.Data.Total != 1 || len(payload.Data.Items) != 1 {
		t.Fatalf("items/total = %d/%d, want 1/1", len(payload.Data.Items), payload.Data.Total)
	}
	if payload.Data.Items[0].Action != audit.ActionAuthLogin {
		t.Fatalf("action = %s, want %s", payload.Data.Items[0].Action, audit.ActionAuthLogin)
	}
}

// TestExportStreamsCSV 验证导出返回 CSV 而不是 JSON 信封。
func TestExportStreamsCSV(t *testing.T) {
	handler := newHandler(t)
	target := "/admin/v1/audit/events/export?from=2026-11-02T00:00:00Z&to=2026-11-02T12:00:00Z"
	rec := httptest.NewRecorder()
	handler.Export(rec, adminRequest(t, http.MethodGet, target))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want 2: %q", len(lines), rec.Body.String())
	}
}

// TestExportRejectsChannelMismatch 验证端别不一致时拒绝（SEC-14）。
func TestExportRejectsChannelMismatch(t *testing.T) {
	handler := newHandler(t)
	target := "/admin/v1/audit/events/export?from=2026-11-02T00:00:00Z&to=2026-11-02T12:00:00Z"
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Channel:   "biz",
		SubjectID: "biz-user",
	}))
	rec := httptest.NewRecorder()
	handler.Export(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

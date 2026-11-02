package app_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// fakeReader 是可编程的审计只读仓储。
type fakeReader struct {
	listRows  []audit.Event
	counted   int
	streamErr error
	listCalls int
	lastList  domain.Filter
}

// List 返回预置事件。
func (r *fakeReader) List(_ context.Context, filter domain.Filter) ([]audit.Event, error) {
	r.listCalls++
	r.lastList = filter
	return r.listRows, nil
}

// Count 返回预置总数。
func (r *fakeReader) Count(_ context.Context, _ domain.Filter) (int, error) {
	return r.counted, nil
}

// Stream 逐条回调预置事件。
func (r *fakeReader) Stream(_ context.Context, _ domain.Filter, emit func(audit.Event) error) error {
	for _, event := range r.listRows {
		if err := emit(event); err != nil {
			return err
		}
	}
	return r.streamErr
}

// seqIDs 是固定序列的 ID 生成器，避免引入雪花依赖。
type seqIDs struct{ next int }

// Next 返回自增 ID。
func (g *seqIDs) Next() (int64, error) {
	g.next++
	return int64(g.next), nil
}

// NextString 返回自增 ID 的字符串形式。
func (g *seqIDs) NextString() (string, error) {
	id, err := g.Next()
	if err != nil {
		return "", err
	}
	return time.Unix(id, 0).UTC().Format("20060102T150405Z"), nil
}

// recordingEmitter 记录发出的审计事件。
type recordingEmitter struct{ events []audit.Event }

// Emit 追加事件。
func (e *recordingEmitter) Emit(_ context.Context, event audit.Event) error {
	e.events = append(e.events, event)
	return nil
}

// fixedFilter 返回一个合法查询条件。
func fixedFilter() domain.Filter {
	to := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	return domain.Filter{From: to.Add(-time.Hour), To: to, Limit: 10}
}

// TestQueryServiceListCapsPageSize 验证分页上限收敛与校验失败路径。
func TestQueryServiceListCapsPageSize(t *testing.T) {
	reader := &fakeReader{listRows: []audit.Event{{EventID: "e1"}}, counted: 1}
	svc, err := app.NewQueryService(reader, nil, &seqIDs{}, app.QueryOptions{
		MaxRange:    24 * time.Hour,
		MaxPageSize: 5,
	}, "admin-api")
	if err != nil {
		t.Fatalf("NewQueryService() error = %v", err)
	}

	filter := fixedFilter()
	filter.Limit = 100
	result, err := svc.List(context.Background(), filter, app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("Total = %d, want 1", result.Total)
	}
	if reader.lastList.Limit != 5 {
		t.Fatalf("reader received limit %d, want capped 5", reader.lastList.Limit)
	}

	invalid := fixedFilter()
	invalid.From = time.Time{}
	if _, err := svc.List(context.Background(), invalid, app.AccessMeta{ActorID: "admin-1", Channel: "admin"}); err == nil {
		t.Fatal("List() with missing window: want error, got nil")
	}
}

// TestQueryServiceExportCSV 验证 CSV 列顺序、行数与 data.export 审计事件。
func TestQueryServiceExportCSV(t *testing.T) {
	occurred := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	reader := &fakeReader{listRows: []audit.Event{
		{
			EventID:      "evt-1",
			Action:       audit.ActionAuthLogin,
			ActorID:      "admin-1",
			ActorType:    "admin",
			Channel:      "admin",
			ResourceType: "session",
			ResourceID:   "s-1",
			Outcome:      audit.OutcomeSuccess,
			OccurredAt:   occurred,
			TraceID:      "trace-1",
			Service:      "admin-api",
		},
	}}
	emitter := &recordingEmitter{}
	svc, err := app.NewQueryService(reader, emitter, &seqIDs{}, app.QueryOptions{
		MaxRange:       24 * time.Hour,
		MaxPageSize:    20,
		ExportMaxRange: 24 * time.Hour,
		ExportMaxRows:  10,
	}, "admin-api")
	if err != nil {
		t.Fatalf("NewQueryService() error = %v", err)
	}

	var buf bytes.Buffer
	result, err := svc.Export(context.Background(), fixedFilter(), app.AccessMeta{
		ActorID:   "admin-1",
		ActorType: "admin",
		Channel:   "admin",
		TraceID:   "trace-1",
	}, &buf)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if result.Rows != 1 {
		t.Fatalf("Rows = %d, want 1", result.Rows)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv lines = %d, want header + 1 row: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "event_id,occurred_at,action") {
		t.Fatalf("csv header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "evt-1,2026-11-02T10:00:00Z,auth.login") {
		t.Fatalf("csv row = %q", lines[1])
	}
	if len(emitter.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Action != audit.ActionDataExport {
		t.Fatalf("audit action = %s, want %s", event.Action, audit.ActionDataExport)
	}
	if event.Attrs["rows"] != "1" {
		t.Fatalf("audit attrs rows = %q, want 1", event.Attrs["rows"])
	}
}

// TestQueryServiceExportRejectsOverflow 验证导出超过行数上限时拒绝而非静默截断。
func TestQueryServiceExportRejectsOverflow(t *testing.T) {
	rows := make([]audit.Event, 3)
	for i := range rows {
		rows[i] = audit.Event{EventID: "e", Action: audit.ActionAuthLogin, OccurredAt: time.Now().UTC()}
	}
	svc, err := app.NewQueryService(&fakeReader{listRows: rows}, nil, &seqIDs{}, app.QueryOptions{
		MaxRange:       24 * time.Hour,
		MaxPageSize:    20,
		ExportMaxRange: 24 * time.Hour,
		ExportMaxRows:  2,
	}, "admin-api")
	if err != nil {
		t.Fatalf("NewQueryService() error = %v", err)
	}

	var buf bytes.Buffer
	_, err = svc.Export(context.Background(), fixedFilter(), app.AccessMeta{
		ActorID: "admin-1", Channel: "admin", TraceID: "trace-1",
	}, &buf)
	if err == nil {
		t.Fatal("Export() over limit: want error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("Export() error = %v, want exceeds maximum", err)
	}
}

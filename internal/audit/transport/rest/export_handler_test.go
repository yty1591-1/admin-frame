package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	auditrest "github.com/admin-frame/admin-frame/internal/audit/transport/rest"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/pagination"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// stubExportStore 只实现 Handler 用到的 Create / Get / MarkSucceeded。
type stubExportStore struct {
	mu   sync.Mutex
	jobs map[string]*domain.ExportJob
}

func newStubExportStore() *stubExportStore {
	return &stubExportStore{jobs: map[string]*domain.ExportJob{}}
}

func (s *stubExportStore) Create(_ context.Context, job domain.ExportJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := job
	s.jobs[job.ID] = &copied
	return nil
}

func (s *stubExportStore) Get(_ context.Context, id string) (domain.ExportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return domain.ExportJob{}, domain.ErrExportNotFound
	}
	return *job, nil
}

// ClaimPending 按创建时间抢占最早的 pending 任务，模拟 SQL 侧的
// 「UPDATE … WHERE status='pending' ORDER BY created_at LIMIT 1」。
func (s *stubExportStore) ClaimPending(_ context.Context, now time.Time) (domain.ExportJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var picked *domain.ExportJob
	for _, job := range s.jobs {
		if job.Status != domain.ExportPending {
			continue
		}
		if picked == nil || job.CreatedAt.Before(picked.CreatedAt) {
			picked = job
		}
	}
	if picked == nil {
		return domain.ExportJob{}, false, nil
	}
	picked.Status = domain.ExportRunning
	picked.Attempts++
	picked.StartedAt = now
	return *picked, true, nil
}

func (s *stubExportStore) RequeueStale(context.Context, time.Time, int) (int, error) { return 0, nil }

func (s *stubExportStore) MarkSucceeded(_ context.Context, id string, rows int, key string, finished time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return domain.ErrExportNotFound
	}
	job.Status = domain.ExportSucceeded
	job.RowCount = rows
	job.ObjectKey = key
	job.FinishedAt = finished
	return nil
}

func (s *stubExportStore) MarkFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

func (s *stubExportStore) ListExpired(context.Context, time.Time, int) ([]domain.ExportJob, error) {
	return nil, nil
}

func (s *stubExportStore) MarkExpired(context.Context, []string, time.Time) error { return nil }

// stubObjects 是内存对象存储。
type stubObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newStubObjects() *stubObjects { return &stubObjects{objects: map[string][]byte{}} }

func (o *stubObjects) Put(_ context.Context, key string, body io.Reader, _ storage.PutOptions) (storage.ObjectInfo, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.objects[key] = payload
	return storage.ObjectInfo{Key: key, Size: int64(len(payload))}, nil
}

func (o *stubObjects) Get(_ context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	payload, ok := o.objects[key]
	if !ok {
		return nil, storage.ObjectInfo{}, errors.New("not found")
	}
	return io.NopCloser(strings.NewReader(string(payload))), storage.ObjectInfo{Key: key, Size: int64(len(payload))}, nil
}

func (o *stubObjects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.objects, key)
	return nil
}

func (o *stubObjects) Stat(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("not implemented")
}

func (o *stubObjects) Exists(context.Context, string) (bool, error) { return false, nil }

func (o *stubObjects) List(context.Context, storage.ListOptions) (storage.ListResult, error) {
	return storage.ListResult{}, errors.New("not implemented")
}

func (o *stubObjects) Close() error { return nil }

// newExportHandler 构造带异步导出能力的 Handler，并返回底层服务用于驱动 worker 侧。
func newExportHandler(t *testing.T) (*auditrest.Handler, *app.ExportJobService, *stubExportStore) {
	t.Helper()
	store := newStubExportStore()
	reader := &stubReader{events: []audit.Event{{EventID: "evt-1", Action: audit.ActionAuthLogin}}}
	jobs, err := app.NewExportJobService(app.ExportJobConfig{
		Store:   store,
		Reader:  reader,
		Objects: newStubObjects(),
		IDs:     &stubIDs{},
		Options: app.ExportJobOptions{MaxRange: 24 * time.Hour, MaxRows: 10, TTL: time.Hour},
		Service: "admin-api",
	})
	if err != nil {
		t.Fatalf("NewExportJobService() error = %v", err)
	}
	query, err := app.NewQueryService(reader, nil, &stubIDs{}, app.QueryOptions{
		MaxRange:       24 * time.Hour,
		MaxPageSize:    100,
		ExportMaxRange: 24 * time.Hour,
		ExportMaxRows:  10,
	}, "admin-api")
	if err != nil {
		t.Fatalf("NewQueryService() error = %v", err)
	}
	handler, err := auditrest.NewHandler(auditrest.HandlerConfig{
		Query:          query,
		Jobs:           jobs,
		QueryLimits:    pagination.Config{DefaultPageSize: 20, MaxPageSize: 100},
		MaxRange:       24 * time.Hour,
		ExportMaxRange: 24 * time.Hour,
		Channel:        "admin",
		Service:        "admin-api",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler, jobs, store
}

const exportRangeQuery = "?from=2026-11-02T00:00:00Z&to=2026-11-02T12:00:00Z"

// TestCreateExportReturnsAccepted 验证 POST 返回 202 与任务状态。
func TestCreateExportReturnsAccepted(t *testing.T) {
	handler, _, _ := newExportHandler(t)
	rec := httptest.NewRecorder()
	handler.CreateExport(rec, adminRequest(t, http.MethodPost, "/admin/v1/audit/events/export"+exportRangeQuery))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data auditrest.ExportJobDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Status != string(domain.ExportPending) || envelope.Data.ExportID == "" {
		t.Fatalf("data = %+v, want pending job with id", envelope.Data)
	}
	if envelope.Data.DownloadPath != "" {
		t.Fatalf("download_path = %q, want empty before success", envelope.Data.DownloadPath)
	}
}

// TestCreateExportWithoutAsyncReturnsUnavailable 验证未开启异步导出时返回 503 而不是 404。
func TestCreateExportWithoutAsyncReturnsUnavailable(t *testing.T) {
	handler := newHandler(t) // 该基座不注入 Jobs
	rec := httptest.NewRecorder()
	handler.CreateExport(rec, adminRequest(t, http.MethodPost, "/admin/v1/audit/events/export"+exportRangeQuery))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}

// TestGetExportHidesOtherActor 验证非发起人查询返回 404。
func TestGetExportHidesOtherActor(t *testing.T) {
	handler, _, store := newExportHandler(t)
	rec := httptest.NewRecorder()
	handler.CreateExport(rec, adminRequest(t, http.MethodPost, "/admin/v1/audit/events/export"+exportRangeQuery))
	var created struct {
		Data auditrest.ExportJobDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	exportID := created.Data.ExportID
	if store.jobs[exportID] == nil {
		t.Fatal("job not persisted")
	}

	other := httptest.NewRequest(http.MethodGet, "/admin/v1/audit/exports/"+exportID, http.NoBody)
	other.SetPathValue("id", exportID)
	other = other.WithContext(auth.WithPrincipal(other.Context(), auth.Principal{
		Channel: "admin", SubjectID: "admin-2",
	}))
	rec = httptest.NewRecorder()
	handler.GetExport(rec, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	// 本人查询成功。
	own := httptest.NewRequest(http.MethodGet, "/admin/v1/audit/exports/"+exportID, http.NoBody)
	own.SetPathValue("id", exportID)
	own = own.WithContext(auth.WithPrincipal(own.Context(), auth.Principal{
		Channel: "admin", SubjectID: "admin-1",
	}))
	rec = httptest.NewRecorder()
	handler.GetExport(rec, own)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadExportBeforeReadyReturnsBadRequest 验证未完成任务的下载被拒绝而不是空文件。
func TestDownloadExportBeforeReadyReturnsBadRequest(t *testing.T) {
	handler, _, _ := newExportHandler(t)
	rec := httptest.NewRecorder()
	handler.CreateExport(rec, adminRequest(t, http.MethodPost, "/admin/v1/audit/events/export"+exportRangeQuery))
	var created struct {
		Data auditrest.ExportJobDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	req := adminRequest(t, http.MethodGet, "/admin/v1/audit/exports/"+created.Data.ExportID+"/download")
	req.SetPathValue("id", created.Data.ExportID)
	rec = httptest.NewRecorder()
	handler.DownloadExport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadExportAfterSuccessStreamsCSV 验证任务完成后可下载 CSV 且带附件头。
func TestDownloadExportAfterSuccessStreamsCSV(t *testing.T) {
	handler, jobs, _ := newExportHandler(t)
	rec := httptest.NewRecorder()
	handler.CreateExport(rec, adminRequest(t, http.MethodPost, "/admin/v1/audit/events/export"+exportRangeQuery))
	var created struct {
		Data auditrest.ExportJobDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	// 直接驱动一次执行（等价于 worker 抢占到该任务）。
	if processed, err := jobs.ProcessOnce(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOnce() = (%v, %v), want (true, nil)", processed, err)
	}

	req := adminRequest(t, http.MethodGet, "/admin/v1/audit/exports/"+created.Data.ExportID+"/download")
	req.SetPathValue("id", created.Data.ExportID)
	rec = httptest.NewRecorder()
	handler.DownloadExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	if !strings.Contains(rec.Body.String(), "event_id,occurred_at,action") {
		t.Fatalf("body = %q, want CSV header", rec.Body.String())
	}
}

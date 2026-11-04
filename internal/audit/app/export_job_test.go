package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// fakeExportStore 是内存版导出任务仓储，模拟 pending 抢占与状态流转。
type fakeExportStore struct {
	jobs map[string]*domain.ExportJob
	// claimOrder 保证按创建时间顺序抢占。
	claimOrder []string
}

func newFakeExportStore() *fakeExportStore {
	return &fakeExportStore{jobs: map[string]*domain.ExportJob{}}
}

func (s *fakeExportStore) Create(_ context.Context, job domain.ExportJob) error {
	copied := job
	s.jobs[job.ID] = &copied
	s.claimOrder = append(s.claimOrder, job.ID)
	return nil
}

func (s *fakeExportStore) Get(_ context.Context, id string) (domain.ExportJob, error) {
	job, ok := s.jobs[id]
	if !ok {
		return domain.ExportJob{}, domain.ErrExportNotFound
	}
	return *job, nil
}

func (s *fakeExportStore) ClaimPending(_ context.Context, now time.Time) (domain.ExportJob, bool, error) {
	for _, id := range s.claimOrder {
		job := s.jobs[id]
		if job.Status != domain.ExportPending {
			continue
		}
		job.Status = domain.ExportRunning
		job.StartedAt = now
		job.Attempts++
		return *job, true, nil
	}
	return domain.ExportJob{}, false, nil
}

func (s *fakeExportStore) RequeueStale(_ context.Context, cutoff time.Time, maxAttempts int) (int, error) {
	count := 0
	for _, job := range s.jobs {
		if job.Status != domain.ExportRunning || !job.StartedAt.Before(cutoff) {
			continue
		}
		if job.Attempts >= maxAttempts {
			job.Status = domain.ExportFailed
			job.StartedAt = time.Time{}
			continue
		}
		job.Status = domain.ExportPending
		job.StartedAt = time.Time{}
		count++
	}
	return count, nil
}

func (s *fakeExportStore) MarkSucceeded(_ context.Context, id string, rows int, objectKey string, finishedAt time.Time) error {
	job, ok := s.jobs[id]
	if !ok {
		return domain.ErrExportNotFound
	}
	job.Status = domain.ExportSucceeded
	job.RowCount = rows
	job.ObjectKey = objectKey
	job.FinishedAt = finishedAt
	return nil
}

func (s *fakeExportStore) MarkFailed(_ context.Context, id, reason string, finishedAt time.Time, canRetry bool) error {
	job, ok := s.jobs[id]
	if !ok {
		return domain.ErrExportNotFound
	}
	job.LastError = reason
	if canRetry {
		job.Status = domain.ExportPending
		job.StartedAt = time.Time{}
		return nil
	}
	job.Status = domain.ExportFailed
	job.FinishedAt = finishedAt
	return nil
}

func (s *fakeExportStore) ListExpired(_ context.Context, now time.Time, limit int) ([]domain.ExportJob, error) {
	out := make([]domain.ExportJob, 0, limit)
	for _, job := range s.jobs {
		if job.Status == domain.ExportExpired || now.Before(job.ExpiresAt) {
			continue
		}
		out = append(out, *job)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out, nil
}

func (s *fakeExportStore) MarkExpired(_ context.Context, ids []string, finishedAt time.Time) error {
	for _, id := range ids {
		if job, ok := s.jobs[id]; ok {
			job.Status = domain.ExportExpired
			job.FinishedAt = finishedAt
		}
	}
	return nil
}

// fakeObjects 是内存对象存储；putFailures 用于模拟上传失败。
type fakeObjects struct {
	objects     map[string][]byte
	putFailures int
	deletes     int
}

func newFakeObjects() *fakeObjects {
	return &fakeObjects{objects: map[string][]byte{}}
}

func (o *fakeObjects) Put(_ context.Context, key string, body io.Reader, _ storage.PutOptions) (storage.ObjectInfo, error) {
	if o.putFailures > 0 {
		o.putFailures--
		return storage.ObjectInfo{}, errors.New("object store unavailable")
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	o.objects[key] = payload
	return storage.ObjectInfo{Key: key, Size: int64(len(payload))}, nil
}

func (o *fakeObjects) Get(_ context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	payload, ok := o.objects[key]
	if !ok {
		return nil, storage.ObjectInfo{}, fmt.Errorf("object %s not found", key)
	}
	return io.NopCloser(strings.NewReader(string(payload))), storage.ObjectInfo{Key: key, Size: int64(len(payload))}, nil
}

func (o *fakeObjects) Delete(_ context.Context, key string) error {
	o.deletes++
	delete(o.objects, key)
	return nil
}

func (o *fakeObjects) Stat(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("not implemented")
}

func (o *fakeObjects) Exists(_ context.Context, key string) (bool, error) {
	_, ok := o.objects[key]
	return ok, nil
}

func (o *fakeObjects) List(context.Context, storage.ListOptions) (storage.ListResult, error) {
	return storage.ListResult{}, errors.New("not implemented")
}

func (o *fakeObjects) Close() error { return nil }

func newExportService(t *testing.T, store domain.ExportStore, reader domain.Reader, objects storage.Store, emitter audit.Emitter, opts app.ExportJobOptions) *app.ExportJobService {
	t.Helper()
	if opts.MaxRows == 0 {
		opts.MaxRows = 100
	}
	if opts.MaxRange == 0 {
		opts.MaxRange = 24 * time.Hour
	}
	svc, err := app.NewExportJobService(app.ExportJobConfig{
		Store:   store,
		Reader:  reader,
		Objects: objects,
		Emitter: emitter,
		IDs:     &seqIDs{},
		Options: opts,
		Service: "admin-api",
	})
	if err != nil {
		t.Fatalf("NewExportJobService() error = %v", err)
	}
	return svc
}

// TestExportJobCreateValidatesAndPersists 覆盖创建路径：范围校验、状态与过期时间。
func TestExportJobCreateValidatesAndPersists(t *testing.T) {
	store := newFakeExportStore()
	svc := newExportService(t, store, &fakeReader{}, newFakeObjects(), nil, app.ExportJobOptions{
		MaxRange: time.Hour, MaxRows: 100, TTL: 48 * time.Hour,
	})

	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{
		ActorID: "admin-1", Channel: "admin", TraceID: "trace-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if job.Status != domain.ExportPending {
		t.Fatalf("status = %s, want pending", job.Status)
	}
	if got := job.ExpiresAt.Sub(job.CreatedAt); got != 48*time.Hour {
		t.Fatalf("ttl = %s, want 48h", got)
	}
	if store.jobs[job.ID] == nil {
		t.Fatal("job not persisted")
	}

	tooWide := fixedFilter()
	tooWide.From = tooWide.To.Add(-2 * time.Hour)
	if _, err := svc.Create(context.Background(), tooWide, app.AccessMeta{ActorID: "admin-1", Channel: "admin"}); err == nil {
		t.Fatal("Create() over max range: want error, got nil")
	}
}

// TestExportJobGetOnlyForOwner 验证非发起人一律 404，避免枚举他人导出范围。
func TestExportJobGetOnlyForOwner(t *testing.T) {
	store := newFakeExportStore()
	svc := newExportService(t, store, &fakeReader{}, newFakeObjects(), nil, app.ExportJobOptions{})
	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.Get(context.Background(), job.ID, app.AccessMeta{ActorID: "admin-2", Channel: "admin"}); err == nil {
		t.Fatal("Get() by another actor: want error, got nil")
	} else if !isNotFound(err) {
		t.Fatalf("Get() error = %v, want not found", err)
	}
	if _, err := svc.Get(context.Background(), job.ID, app.AccessMeta{ActorID: "admin-1", Channel: "admin"}); err != nil {
		t.Fatalf("Get() by owner error = %v", err)
	}
}

// TestExportJobProcessSucceeds 覆盖正常执行：CSV 落对象存储、任务置成功、审计事件带行数。
func TestExportJobProcessSucceeds(t *testing.T) {
	store := newFakeExportStore()
	objects := newFakeObjects()
	emitter := &recordingEmitter{}
	reader := &fakeReader{
		listRows: []audit.Event{{EventID: "e1"}, {EventID: "e2"}},
		counted:  2,
	}
	svc := newExportService(t, store, reader, objects, emitter, app.ExportJobOptions{
		MaxRange: 24 * time.Hour, MaxRows: 10, TTL: time.Hour,
	})
	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	processed, err := svc.ProcessOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOnce() = (%v, %v), want (true, nil)", processed, err)
	}

	stored := store.jobs[job.ID]
	if stored.Status != domain.ExportSucceeded {
		t.Fatalf("status = %s (error %q), want succeeded", stored.Status, stored.LastError)
	}
	if stored.RowCount != 2 {
		t.Fatalf("rows = %d, want 2", stored.RowCount)
	}
	payload, ok := objects.objects[stored.ObjectKey]
	if !ok {
		t.Fatalf("object %q not written", stored.ObjectKey)
	}
	if !strings.HasPrefix(string(payload), "event_id,occurred_at,action") {
		t.Fatalf("csv header = %q", firstLine(string(payload)))
	}
	if len(emitter.events) == 0 {
		t.Fatal("no audit event emitted")
	}
	event := emitter.events[len(emitter.events)-1]
	if event.Action != audit.ActionDataExport || event.Attrs["rows"] != "2" || event.ResourceID != job.ID {
		t.Fatalf("audit event = %+v", event)
	}

	// 下载：成功任务可读，且写入一条 async-download 审计。
	_, body, err := svc.Open(context.Background(), job.ID, app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatal("download body differs from stored object")
	}
}

// TestExportJobProcessFailsWhenRowsExceedLimit 验证超限是终态失败（不重试、不留文件）。
func TestExportJobProcessFailsWhenRowsExceedLimit(t *testing.T) {
	store := newFakeExportStore()
	objects := newFakeObjects()
	emitter := &recordingEmitter{}
	reader := &fakeReader{counted: 11}
	svc := newExportService(t, store, reader, objects, emitter, app.ExportJobOptions{
		MaxRange: 24 * time.Hour, MaxRows: 10, RetryMax: 3, TTL: time.Hour,
	})
	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.ProcessOnce(context.Background()); err == nil {
		t.Fatal("ProcessOnce() over limit: want error, got nil")
	}
	stored := store.jobs[job.ID]
	if stored.Status != domain.ExportFailed {
		t.Fatalf("status = %s, want failed（超限不应重试）", stored.Status)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("objects = %d, want 0（超限不应留半截文件）", len(objects.objects))
	}
	if len(emitter.events) != 1 || emitter.events[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("events = %+v, want one failure event", emitter.events)
	}
}

// TestExportJobRetriesThenSucceeds 验证可恢复错误按 RetryMax 重排，最后一次成功。
func TestExportJobRetriesThenSucceeds(t *testing.T) {
	store := newFakeExportStore()
	objects := newFakeObjects()
	objects.putFailures = 1
	reader := &fakeReader{listRows: []audit.Event{{EventID: "e1"}}, counted: 1}
	svc := newExportService(t, store, reader, objects, &recordingEmitter{}, app.ExportJobOptions{
		MaxRange: 24 * time.Hour, MaxRows: 10, RetryMax: 2, TTL: time.Hour,
	})
	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.ProcessOnce(context.Background()); err == nil {
		t.Fatal("first ProcessOnce(): want upload error, got nil")
	}
	if store.jobs[job.ID].Status != domain.ExportPending {
		t.Fatalf("status after first failure = %s, want pending (attempts < retry_max)", store.jobs[job.ID].Status)
	}

	if _, err := svc.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce() error = %v", err)
	}
	if store.jobs[job.ID].Status != domain.ExportSucceeded {
		t.Fatalf("status = %s, want succeeded", store.jobs[job.ID].Status)
	}
}

// TestExportJobExpireDeletesObject 验证到期清理删除对象并置 expired。
func TestExportJobExpireDeletesObject(t *testing.T) {
	store := newFakeExportStore()
	objects := newFakeObjects()
	reader := &fakeReader{listRows: []audit.Event{{EventID: "e1"}}, counted: 1}
	svc := newExportService(t, store, reader, objects, nil, app.ExportJobOptions{
		MaxRange: 24 * time.Hour, MaxRows: 10, TTL: time.Hour,
	})
	job, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	key := store.jobs[job.ID].ObjectKey

	// 把过期时间推到过去，模拟到达保留期。
	store.jobs[job.ID].ExpiresAt = time.Now().UTC().Add(-time.Minute)
	count, err := svc.ExpireOnce(context.Background())
	if err != nil {
		t.Fatalf("ExpireOnce() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expired = %d, want 1", count)
	}
	if _, ok := objects.objects[key]; ok {
		t.Fatal("object still present after expiry")
	}
	if store.jobs[job.ID].Status != domain.ExportExpired {
		t.Fatalf("status = %s, want expired", store.jobs[job.ID].Status)
	}
}

// isNotFound 判断是否为 404 业务码。
func isNotFound(err error) bool {
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		return appErr.NumericCode() == apperr.CodeNotFound
	}
	return false
}

// firstLine 取首行，便于断言 CSV 表头。
func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

// TestExportJobRequeueStaleStopsPoisonPill 验证卡死任务按重试次数分流：
// 用尽次数的置失败，否则重排。混为一谈会让崩溃任务永远是最早的 pending，
// 把后续导出全部饿死（ClaimPending 按 created_at 取最早一条）。
func TestExportJobRequeueStaleStopsPoisonPill(t *testing.T) {
	store := newFakeExportStore()
	svc := newExportService(t, store, &fakeReader{}, newFakeObjects(), nil, app.ExportJobOptions{
		MaxRange: 24 * time.Hour, MaxRows: 10, RetryMax: 2, TTL: time.Hour,
		StaleTimeout: time.Minute,
	})
	stuck := time.Now().UTC().Add(-time.Hour)

	poison, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.jobs[poison.ID].Status = domain.ExportRunning
	store.jobs[poison.ID].StartedAt = stuck
	store.jobs[poison.ID].Attempts = 2

	retryable, err := svc.Create(context.Background(), fixedFilter(), app.AccessMeta{ActorID: "admin-1", Channel: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	store.jobs[retryable.ID].Status = domain.ExportRunning
	store.jobs[retryable.ID].StartedAt = stuck
	store.jobs[retryable.ID].Attempts = 1

	count, err := svc.RequeueStale(context.Background())
	if err != nil {
		t.Fatalf("RequeueStale() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("requeued = %d, want 1", count)
	}
	if got := store.jobs[poison.ID].Status; got != domain.ExportFailed {
		t.Fatalf("poison status = %s, want failed", got)
	}
	if got := store.jobs[retryable.ID].Status; got != domain.ExportPending {
		t.Fatalf("retryable status = %s, want pending", got)
	}
}

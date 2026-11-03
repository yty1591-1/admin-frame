package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// 导出任务的默认参数，未配置时生效。
const (
	defaultExportTTL          = 7 * 24 * time.Hour
	defaultExportStaleTimeout = 10 * time.Minute
	defaultExportObjectPrefix = "audit/exports"
)

// ExportJobOptions 是异步导出的执行参数，来自 audit.export 配置。
type ExportJobOptions struct {
	// MaxRange 单次导出的最大时间跨度。
	MaxRange time.Duration
	// MaxRows 单次导出的最大行数。
	MaxRows int
	// TTL 文件与任务记录的保留时长。
	TTL time.Duration
	// RetryMax 单个任务的最大执行次数（含首次）。
	RetryMax int
	// StaleTimeout running 状态超过该时长未完成即视为 worker 失效，重新排队。
	StaleTimeout time.Duration
	// ObjectPrefix 对象键前缀，便于按前缀做生命周期与容量治理。
	ObjectPrefix string
}

// ExportJobConfig 是导出任务服务的装配参数。
type ExportJobConfig struct {
	// Store 任务仓储，必填。
	Store domain.ExportStore
	// Reader 审计只读仓储，必填（复用同步导出的取数与 CSV 列定义）。
	Reader domain.Reader
	// Objects 对象存储，必填；导出文件落库外，避免大结果拖垮业务库。
	Objects storage.Store
	// Emitter 审计事件出口；nil 时只写结构化日志。
	Emitter audit.Emitter
	// IDs 事件 ID 生成器，必填。
	IDs id.Generator
	// Options 执行参数。
	Options ExportJobOptions
	// Service 服务名，写入审计事件。
	Service string
	// Logger 结构化日志；nil 时静默。
	Logger *slog.Logger
}

// ExportJobService 提供审计异步导出的创建、查询与执行。
type ExportJobService struct {
	store   domain.ExportStore
	reader  domain.Reader
	objects storage.Store
	emitter audit.Emitter
	ids     id.Generator
	opts    ExportJobOptions
	service string
	logger  *slog.Logger
	now     func() time.Time
}

// NewExportJobService 构造导出任务服务。
func NewExportJobService(cfg ExportJobConfig) (*ExportJobService, error) {
	switch {
	case cfg.Store == nil:
		return nil, apperr.Internal("audit export service requires a job store", nil)
	case cfg.Reader == nil:
		return nil, apperr.Internal("audit export service requires a reader", nil)
	case cfg.Objects == nil:
		return nil, apperr.Internal("audit export service requires an object store", nil)
	case cfg.IDs == nil:
		return nil, apperr.Internal("audit export service requires an id generator", nil)
	}
	opts := cfg.Options
	if opts.MaxRows <= 0 {
		return nil, apperr.Internal("audit export max rows is not configured", nil)
	}
	if opts.TTL <= 0 {
		opts.TTL = defaultExportTTL
	}
	if opts.StaleTimeout <= 0 {
		opts.StaleTimeout = defaultExportStaleTimeout
	}
	if opts.RetryMax <= 0 {
		opts.RetryMax = 1
	}
	if strings.TrimSpace(opts.ObjectPrefix) == "" {
		opts.ObjectPrefix = defaultExportObjectPrefix
	}
	return &ExportJobService{
		store:   cfg.Store,
		reader:  cfg.Reader,
		objects: cfg.Objects,
		emitter: cfg.Emitter,
		ids:     cfg.IDs,
		opts:    opts,
		service: cfg.Service,
		logger:  cfg.Logger,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// Create 创建导出任务（pending），由 worker 异步执行。
//
// 只做「能不能导」的校验（时间窗、行数上限、操作者），真正取数在 worker；
// 这样大范围导出不会占用 API 请求线程。
func (s *ExportJobService) Create(ctx context.Context, filter domain.Filter, meta AccessMeta) (domain.ExportJob, error) {
	if filter.Offset != 0 {
		return domain.ExportJob{}, apperr.BadRequest("export does not support offset")
	}
	if err := filter.Validate(s.opts.MaxRange); err != nil {
		return domain.ExportJob{}, apperr.BadRequest(err.Error())
	}
	if strings.TrimSpace(meta.ActorID) == "" || strings.TrimSpace(meta.Channel) == "" {
		return domain.ExportJob{}, apperr.BadRequest("export actor and channel are required")
	}
	exportID, err := s.ids.NextString()
	if err != nil {
		return domain.ExportJob{}, apperr.Internal("generate export id", err)
	}
	now := s.now()
	job := domain.ExportJob{
		ID:         exportID,
		ActorID:    strings.TrimSpace(meta.ActorID),
		ActorType:  meta.ActorType,
		Channel:    strings.TrimSpace(meta.Channel),
		MerchantID: meta.MerchantID,
		TraceID:    meta.TraceID,
		Filter:     domain.Snapshot(filter),
		Status:     domain.ExportPending,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.opts.TTL),
	}
	if err := s.store.Create(ctx, job); err != nil {
		return domain.ExportJob{}, apperr.Internal("create export job", err)
	}
	return job, nil
}

// Get 读取任务状态；只允许发起人本人查看。
//
// 不区分「不存在」与「非本人」：否则任何持有 audit:read 的账号都能枚举
// 他人的导出范围（时间窗与过滤条件本身也是敏感信息）。
func (s *ExportJobService) Get(ctx context.Context, exportID string, meta AccessMeta) (domain.ExportJob, error) {
	job, err := s.store.Get(ctx, strings.TrimSpace(exportID))
	if errors.Is(err, domain.ErrExportNotFound) {
		return domain.ExportJob{}, apperr.NotFound("export job not found")
	}
	if err != nil {
		return domain.ExportJob{}, apperr.Internal("get export job", err)
	}
	if job.ActorID != strings.TrimSpace(meta.ActorID) {
		return domain.ExportJob{}, apperr.NotFound("export job not found")
	}
	return job, nil
}

// Open 打开已完成的导出文件用于下载，并记录下载审计。
//
// 行数、时间窗等元数据随任务返回，调用方可据此设置响应头。
func (s *ExportJobService) Open(ctx context.Context, exportID string, meta AccessMeta) (domain.ExportJob, []byte, error) {
	job, err := s.Get(ctx, exportID, meta)
	if err != nil {
		return domain.ExportJob{}, nil, err
	}
	if !job.Downloadable(s.now()) {
		return domain.ExportJob{}, nil, apperr.BadRequest(
			fmt.Sprintf("export job is not downloadable in status %s", job.Status))
	}
	body, err := readAll(ctx, s.objects, job.ObjectKey)
	if err != nil {
		return domain.ExportJob{}, nil, apperr.Internal("read export object", err)
	}
	if emitErr := emitAccessEvent(ctx, s.emitter, s.ids, s.service, audit.ActionDataExport,
		job.ID, meta, audit.OutcomeSuccess, map[string]string{
			"rows":      strconv.Itoa(job.RowCount),
			"mode":      "async-download",
			"from":      job.Filter.From.UTC().Format(time.RFC3339),
			"to":        job.Filter.To.UTC().Format(time.RFC3339),
			"filters":   describeSnapshot(job.Filter),
			"export_id": job.ID,
		}); emitErr != nil {
		// 文件已读出，无法回滚；返回错误由上层记录，不静默吞掉（AUD-03）。
		return job, body, apperr.Internal("emit audit export download event", emitErr)
	}
	return job, body, nil
}

// ProcessOnce 抢占并执行一条待处理的导出任务。
//
// 返回 processed=false 表示当前没有待执行任务。执行失败按 attempts 与
// RetryMax 决定重排还是置为 failed：重试只针对可恢复错误（DB/存储抖动），
// 「超过行数上限」这类确定性失败重试没有意义。
func (s *ExportJobService) ProcessOnce(ctx context.Context) (bool, error) {
	job, found, err := s.store.ClaimPending(ctx, s.now())
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if runErr := s.execute(ctx, job); runErr != nil {
		canRetry := job.Attempts < s.opts.RetryMax && !errors.Is(runErr, ErrExportTooLarge)
		if markErr := s.store.MarkFailed(ctx, job.ID, runErr.Error(), s.now(), canRetry); markErr != nil {
			return true, fmt.Errorf("export job %s failed (%v) and could not record failure: %w", job.ID, runErr, markErr)
		}
		if !canRetry {
			if emitErr := s.emitOutcome(ctx, job, audit.OutcomeFailure, 0, runErr.Error()); emitErr != nil {
				return true, fmt.Errorf("export job %s failed (%v) and audit emit failed: %w", job.ID, runErr, emitErr)
			}
		}
		return true, fmt.Errorf("export job %s: %w", job.ID, runErr)
	}
	return true, nil
}

// execute 生成 CSV 并上传对象存储，成功后置为 succeeded。
func (s *ExportJobService) execute(ctx context.Context, job domain.ExportJob) error {
	filter := job.Filter.Filter(s.opts.MaxRows)
	if err := filter.Validate(s.opts.MaxRange); err != nil {
		return fmt.Errorf("%w: stored filter is invalid: %v", ErrExportTooLarge, err)
	}

	// 先统计再导出：Count 已超限时不必生成半截文件。
	total, err := s.reader.Count(ctx, filter)
	if err != nil {
		return fmt.Errorf("count export rows: %w", err)
	}
	if total > s.opts.MaxRows {
		return fmt.Errorf("%w: %d rows in range exceeds %d", ErrExportTooLarge, total, s.opts.MaxRows)
	}

	// 内存缓冲而非流式直传：中途超限时不会在对象存储留下半截文件。
	// 上限由 max_rows 约束（默认 10 万行，约 20MB），单 worker 串行执行可接受。
	buf := &bytes.Buffer{}
	rows, err := writeCSV(ctx, s.reader, filter, s.opts.MaxRows, buf)
	if err != nil {
		return err
	}

	key := s.objectKey(job)
	if _, err := s.objects.Put(ctx, key, bytes.NewReader(buf.Bytes()), storage.PutOptions{
		ContentType: "text/csv; charset=utf-8",
		Metadata: map[string]string{
			"export_id": job.ID,
			"actor_id":  job.ActorID,
		},
	}); err != nil {
		return fmt.Errorf("upload export object: %w", err)
	}

	if err := s.store.MarkSucceeded(ctx, job.ID, rows, key, s.now()); err != nil {
		// 对象已上传但任务未置成功：删除对象避免孤儿文件，失败留给重试。
		_ = s.objects.Delete(ctx, key)
		return fmt.Errorf("mark export job succeeded: %w", err)
	}
	if emitErr := s.emitOutcome(ctx, job, audit.OutcomeSuccess, rows, ""); emitErr != nil {
		return fmt.Errorf("emit export audit event: %w", emitErr)
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "audit export job finished",
			"export_id", job.ID, "rows", rows, "actor_id", job.ActorID, "key", key)
	}
	return nil
}

// ExpireOnce 删除到期导出文件并把任务置为 expired。
func (s *ExportJobService) ExpireOnce(ctx context.Context) (int, error) {
	now := s.now()
	jobs, err := s.store.ListExpired(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if strings.TrimSpace(job.ObjectKey) != "" {
			if delErr := s.objects.Delete(ctx, job.ObjectKey); delErr != nil {
				// 删除失败不阻塞置位：下一轮还会再遇到，避免对象泄漏演变成任务永久 pending。
				if s.logger != nil {
					s.logger.WarnContext(ctx, "audit export object delete failed",
						"export_id", job.ID, "key", job.ObjectKey, "error", delErr)
				}
			}
		}
		ids = append(ids, job.ID)
	}
	if err := s.store.MarkExpired(ctx, ids, now); err != nil {
		return 0, err
	}
	if s.logger != nil {
		s.logger.InfoContext(ctx, "audit export jobs expired", "count", len(ids))
	}
	return len(ids), nil
}

// RequeueStale 回收卡在 running 的任务（worker 崩溃恢复）。
func (s *ExportJobService) RequeueStale(ctx context.Context) (int, error) {
	cutoff := s.now().Add(-s.opts.StaleTimeout)
	count, err := s.store.RequeueStale(ctx, cutoff, s.opts.RetryMax)
	if err != nil {
		return 0, err
	}
	if count > 0 && s.logger != nil {
		s.logger.WarnContext(ctx, "audit export jobs requeued after stale timeout", "count", count)
	}
	return count, nil
}

// emitOutcome 记录一次异步导出的最终结果。
func (s *ExportJobService) emitOutcome(ctx context.Context, job domain.ExportJob, outcome audit.Outcome, rows int, reason string) error {
	attrs := map[string]string{
		"mode":      "async",
		"export_id": job.ID,
		"rows":      strconv.Itoa(rows),
		"from":      job.Filter.From.UTC().Format(time.RFC3339),
		"to":        job.Filter.To.UTC().Format(time.RFC3339),
		"filters":   describeSnapshot(job.Filter),
	}
	if reason != "" {
		attrs["reason"] = reason
	}
	return emitAccessEvent(ctx, s.emitter, s.ids, s.service, audit.ActionDataExport,
		job.ID, AccessMeta{
			ActorID:    job.ActorID,
			ActorType:  job.ActorType,
			Channel:    job.Channel,
			MerchantID: job.MerchantID,
			TraceID:    job.TraceID,
		}, outcome, attrs)
}

// objectKey 生成对象键：前缀/日期/导出 ID.csv，便于按天做生命周期治理。
func (s *ExportJobService) objectKey(job domain.ExportJob) string {
	return fmt.Sprintf("%s/%s/%s.csv", strings.TrimSuffix(s.opts.ObjectPrefix, "/"),
		s.now().Format("2006/01/02"), job.ID)
}

// describeSnapshot 汇总过滤条件名称（不含结果数据），与同步导出口径一致。
func describeSnapshot(snapshot domain.FilterSnapshot) string {
	names := make([]string, 0, 8)
	for _, item := range []struct {
		name  string
		value string
	}{
		{"actor_id", snapshot.ActorID},
		{"action", snapshot.Action},
		{"channel", snapshot.Channel},
		{"outcome", snapshot.Outcome},
		{"merchant_id", snapshot.MerchantID},
		{"resource_type", snapshot.ResourceType},
		{"resource_id", snapshot.ResourceID},
		{"trace_id", snapshot.TraceID},
	} {
		if strings.TrimSpace(item.value) != "" {
			names = append(names, item.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// readAll 读取对象全文；导出文件受 max_rows 约束，体量可控。
func readAll(ctx context.Context, store storage.Store, key string) ([]byte, error) {
	rc, info, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rc.Close()
	}()
	buf := &bytes.Buffer{}
	if info.Size > 0 {
		buf.Grow(int(info.Size))
	}
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

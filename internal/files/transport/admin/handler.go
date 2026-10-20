package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/files/app"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
)

// maxUploadFormBytes 限制 multipart 表单整体读入内存的上限。
const maxUploadFormBytes = 32 << 20

// Handler A 端文件 HTTP。
type Handler struct {
	svc *app.Service
}

// NewHandler 构造 Handler。
func NewHandler(svc *app.Service) *Handler {
	return &Handler{svc: svc}
}

// PresignUpload POST /admin/v1/files/uploads/presign
func (h *Handler) PresignUpload(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	var req PresignUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.FromError(w, apperr.BadRequest("invalid json"), traceID)
		return
	}
	res, err := h.svc.PresignUpload(r.Context(), req.Category, req.Filename, req.ContentType)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, res, traceID)
}

// Upload POST /admin/v1/files （multipart: file + category）
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFormBytes)
	//nolint:gosec // G120 的污点分析不识别 MaxBytesReader，请求体大小已在上方限制
	if err := r.ParseMultipartForm(maxUploadFormBytes); err != nil {
		httpx.FromError(w, apperr.BadRequest("invalid multipart form"), traceID)
		return
	}
	category := r.FormValue("category")
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.FromError(w, apperr.BadRequest("file is required"), traceID)
		return
	}
	defer func() { _ = file.Close() }()
	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = r.FormValue("content_type")
	}
	info, err := h.svc.Upload(r.Context(), category, hdr.Filename, ct, file)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, UploadResponse{
		Key:         info.Key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
	}, traceID)
}

// PresignDownload GET /admin/v1/files/downloads/presign?key=
func (h *Handler) PresignDownload(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		httpx.FromError(w, apperr.BadRequest("key is required"), traceID)
		return
	}
	res, err := h.svc.PresignDownload(r.Context(), key)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, res, traceID)
}

// Download GET /admin/v1/files/object?key=
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	traceID := platform.TraceIDFromContext(r.Context())
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		httpx.FromError(w, apperr.BadRequest("key is required"), traceID)
		return
	}
	rc, info, err := h.svc.OpenDownload(r.Context(), key)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	defer func() { _ = rc.Close() }()
	if info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	}
	w.Header().Set("X-Object-Key", info.Key)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

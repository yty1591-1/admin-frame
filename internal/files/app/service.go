// Package app 实现文件预签名、上传、下载等用例编排。
package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Config 文件用例配置。
type Config struct {
	// AllowedContentTypes 允许的 MIME；空则用默认白名单。
	AllowedContentTypes []string
	// MaxBytes 服务端直传上限；0 用 storage 默认。
	MaxBytes int64
	// PresignPutTTL / PresignGetTTL；≤0 用 15m。
	PresignPutTTL time.Duration
	PresignGetTTL time.Duration
}

// Service 附件上传/下载用例。
type Service struct {
	store    storage.Store
	allowCT  map[string]struct{}
	maxBytes int64
	putTTL   time.Duration
	getTTL   time.Duration
}

// NewService 构造用例服务。
func NewService(store storage.Store, cfg Config) *Service {
	allow := cfg.AllowedContentTypes
	if len(allow) == 0 {
		allow = defaultAllowedCT()
	}
	m := make(map[string]struct{}, len(allow))
	for _, ct := range allow {
		m[strings.ToLower(strings.TrimSpace(ct))] = struct{}{}
	}
	putTTL := cfg.PresignPutTTL
	if putTTL <= 0 {
		putTTL = 15 * time.Minute
	}
	getTTL := cfg.PresignGetTTL
	if getTTL <= 0 {
		getTTL = 15 * time.Minute
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = storage.DefaultMaxObjectBytes
	}
	return &Service{
		store:    store,
		allowCT:  m,
		maxBytes: maxBytes,
		putTTL:   putTTL,
		getTTL:   getTTL,
	}
}

// PresignUploadResult 预签名上传结果。
type PresignUploadResult struct {
	Key          string `json:"key"`
	UploadURL    string `json:"upload_url"`
	ContentType  string `json:"content_type"`
	ExpiresInSec int64  `json:"expires_in_sec"`
}

// PresignDownloadResult 预签名下载结果。
type PresignDownloadResult struct {
	Key          string `json:"key"`
	DownloadURL  string `json:"download_url"`
	ExpiresInSec int64  `json:"expires_in_sec"`
}

// PresignUpload 生成直传 URL；无 Presigner 时返回 BadRequest 提示走服务端上传。
func (s *Service) PresignUpload(ctx context.Context, category, filename, contentType string) (*PresignUploadResult, error) {
	ct, err := s.checkContentType(contentType)
	if err != nil {
		return nil, err
	}
	key, err := s.buildKey(category, filename)
	if err != nil {
		return nil, err
	}
	ps, ok := storage.AsPresigner(s.store)
	if !ok {
		return nil, apperr.BadRequest("presign not supported by current storage driver; use POST /admin/v1/files")
	}
	url, err := ps.PresignPut(ctx, key, s.putTTL, storage.PutOptions{ContentType: ct})
	if err != nil {
		return nil, mapStorageErr(err)
	}
	return &PresignUploadResult{
		Key:          key,
		UploadURL:    url,
		ContentType:  ct,
		ExpiresInSec: int64(s.putTTL.Seconds()),
	}, nil
}

// PresignDownload 生成短时下载 URL。
func (s *Service) PresignDownload(ctx context.Context, key string) (*PresignDownloadResult, error) {
	if err := storage.ValidateKey(key); err != nil {
		return nil, apperr.BadRequest("invalid key")
	}
	if !strings.HasPrefix(key, "files/") {
		return nil, apperr.Forbidden("key out of files namespace")
	}
	ps, ok := storage.AsPresigner(s.store)
	if !ok {
		return nil, apperr.BadRequest("presign not supported by current storage driver; use GET /admin/v1/files/object")
	}
	if _, err := s.store.Stat(ctx, key); err != nil {
		return nil, mapStorageErr(err)
	}
	url, err := ps.PresignGet(ctx, key, s.getTTL)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	return &PresignDownloadResult{
		Key:          key,
		DownloadURL:  url,
		ExpiresInSec: int64(s.getTTL.Seconds()),
	}, nil
}

// Upload 服务端中转上传（memory/local 或小文件）。
func (s *Service) Upload(ctx context.Context, category, filename, contentType string, body io.Reader) (storage.ObjectInfo, error) {
	ct, err := s.checkContentType(contentType)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	key, err := s.buildKey(category, filename)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info, err := s.store.Put(ctx, key, body, storage.PutOptions{
		ContentType: ct,
		MaxBytes:    s.maxBytes,
	})
	if err != nil {
		return storage.ObjectInfo{}, mapStorageErr(err)
	}
	return info, nil
}

// OpenDownload 服务端流式下载。
func (s *Service) OpenDownload(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := storage.ValidateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, apperr.BadRequest("invalid key")
	}
	if !strings.HasPrefix(key, "files/") {
		return nil, storage.ObjectInfo{}, apperr.Forbidden("key out of files namespace")
	}
	rc, info, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, mapStorageErr(err)
	}
	return rc, info, nil
}

func (s *Service) checkContentType(ct string) (string, error) {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return "", apperr.BadRequest("content_type is required")
	}
	// 去掉 "; charset=" 等参数
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if _, ok := s.allowCT[ct]; !ok {
		return "", apperr.BadRequest("content_type not allowed")
	}
	return ct, nil
}

func (s *Service) buildKey(category, filename string) (string, error) {
	cat := sanitizeSegment(category)
	if cat == "" {
		cat = "misc"
	}
	name := sanitizeFilename(filename)
	if name == "" {
		return "", apperr.BadRequest("filename is required")
	}
	id, err := crypto.RandomBytes(16)
	if err != nil {
		return "", apperr.Internal("random id", err)
	}
	now := time.Now().UTC()
	key := fmt.Sprintf("files/%s/%04d/%02d/%s_%s", cat, now.Year(), int(now.Month()), hex.EncodeToString(id), name)
	if err := storage.ValidateKey(key); err != nil {
		return "", apperr.BadRequest("invalid key")
	}
	return key, nil
}

func sanitizeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_' || r == ' ':
			if r == ' ' {
				b.WriteByte('_')
			} else {
				b.WriteRune(r)
			}
		}
	}
	out := b.String()
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

func sanitizeSegment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func defaultAllowedCT() []string {
	return []string{
		"application/pdf",
		"image/png",
		"image/jpeg",
		"image/webp",
		"text/csv",
		"application/json",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
}

func mapStorageErr(err error) error {
	if err == nil {
		return nil
	}
	switch err {
	case storage.ErrNotFound:
		return apperr.NotFound("object not found")
	case storage.ErrInvalidKey, storage.ErrInvalidConfig:
		return apperr.BadRequest(err.Error())
	case storage.ErrTooLarge:
		return apperr.BadRequest("object too large")
	case storage.ErrUnsupported:
		return apperr.BadRequest(err.Error())
	default:
		return apperr.Internal("storage error", err)
	}
}

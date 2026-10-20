package cos

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"

	"github.com/admin-frame/admin-frame/pkg/storage"
)

// Put 实现 storage.Store。
func (s *Store) Put(ctx context.Context, key string, body io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	if body == nil {
		body = strings.NewReader("")
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	_, err := s.client.Object.Put(ctx, key, body, &tencentcos.ObjectPutOptions{
		ObjectPutHeaderOptions: &tencentcos.ObjectPutHeaderOptions{
			ContentType: ct,
			XCosMetaXXX: metaHeader(opts.Metadata),
		},
	})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	info, err := s.Stat(ctx, key)
	if err != nil {
		return storage.ObjectInfo{
			Key:         key,
			ContentType: ct,
			Metadata:    cloneMap(opts.Metadata),
		}, nil
	}
	return info, nil
}

// Get 实现 storage.Store。
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	resp, err := s.client.Object.Get(ctx, key, nil)
	if err != nil {
		return nil, storage.ObjectInfo{}, mapErr(err)
	}
	info := objectInfoFromHeader(key, resp.Header)
	return resp.Body, info, nil
}

// Delete 实现 storage.Store。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.Object.Delete(ctx, key)
	return mapErr(err)
}

// Stat 实现 storage.Store。
func (s *Store) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := storage.ValidateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := s.client.Object.Head(ctx, key, nil)
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return objectInfoFromHeader(key, resp.Header), nil
}

// Exists 实现 storage.Store。
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// List 实现 storage.Store。
func (s *Store) List(ctx context.Context, opts storage.ListOptions) (storage.ListResult, error) {
	if err := s.ready(ctx); err != nil {
		return storage.ListResult{}, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	in := &tencentcos.BucketGetOptions{
		MaxKeys: limit,
		Prefix:  opts.Prefix,
		Marker:  opts.ContinuationToken,
	}
	out, _, err := s.client.Bucket.Get(ctx, in)
	if err != nil {
		return storage.ListResult{}, mapErr(err)
	}
	items := make([]storage.ObjectInfo, 0, len(out.Contents))
	for _, obj := range out.Contents {
		items = append(items, storage.ObjectInfo{
			Key:  obj.Key,
			Size: obj.Size,
			ETag: trimETag(obj.ETag),
		})
	}
	res := storage.ListResult{
		Items:       items,
		IsTruncated: out.IsTruncated,
	}
	if res.IsTruncated {
		res.NextToken = out.NextMarker
	}
	return res, nil
}

// Close 实现 storage.Store。
func (s *Store) Close() error {
	if s != nil {
		s.closed.Store(true)
	}
	return nil
}

// PresignPut 实现 storage.Presigner。
func (s *Store) PresignPut(ctx context.Context, key string, expiry time.Duration, opts storage.PutOptions) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = s.putTTL
	}
	ct := opts.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	presignOpt := &tencentcos.PresignedURLOptions{
		Header: &http.Header{},
	}
	presignOpt.Header.Set("Content-Type", ct)
	u, err := s.client.Object.GetPresignedURL(ctx, http.MethodPut, key, s.ak, s.sk, expiry, presignOpt)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignGet 实现 storage.Presigner。
func (s *Store) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := s.ready(ctx); err != nil {
		return "", err
	}
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = s.getTTL
	}
	u, err := s.client.Object.GetPresignedURL(ctx, http.MethodGet, key, s.ak, s.sk, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func objectInfoFromHeader(key string, h http.Header) storage.ObjectInfo {
	info := storage.ObjectInfo{
		Key:         key,
		ContentType: h.Get("Content-Type"),
		ETag:        trimETag(h.Get("ETag")),
		Metadata:    parseMeta(h),
	}
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			info.Size = n
		}
	}
	if lm := h.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			info.LastModified = t
		}
	}
	return info
}

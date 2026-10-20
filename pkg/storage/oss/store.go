package oss

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"

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
	_, err := s.client.PutObject(ctx, &alioss.PutObjectRequest{
		Bucket:      alioss.Ptr(s.bucket),
		Key:         alioss.Ptr(key),
		Body:        body,
		ContentType: alioss.Ptr(ct),
		Metadata:    opts.Metadata,
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
	out, err := s.client.GetObject(ctx, &alioss.GetObjectRequest{
		Bucket: alioss.Ptr(s.bucket),
		Key:    alioss.Ptr(key),
	})
	if err != nil {
		return nil, storage.ObjectInfo{}, mapErr(err)
	}
	info := storage.ObjectInfo{
		Key:          key,
		Size:         out.ContentLength,
		ContentType:  alioss.ToString(out.ContentType),
		ETag:         trimETag(alioss.ToString(out.ETag)),
		Metadata:     cloneMap(out.Metadata),
		LastModified: alioss.ToTime(out.LastModified),
	}
	return out.Body, info, nil
}

// Delete 实现 storage.Store。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := storage.ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &alioss.DeleteObjectRequest{
		Bucket: alioss.Ptr(s.bucket),
		Key:    alioss.Ptr(key),
	})
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
	out, err := s.client.HeadObject(ctx, &alioss.HeadObjectRequest{
		Bucket: alioss.Ptr(s.bucket),
		Key:    alioss.Ptr(key),
	})
	if err != nil {
		return storage.ObjectInfo{}, mapErr(err)
	}
	return storage.ObjectInfo{
		Key:          key,
		Size:         out.ContentLength,
		ContentType:  alioss.ToString(out.ContentType),
		ETag:         trimETag(alioss.ToString(out.ETag)),
		Metadata:     cloneMap(out.Metadata),
		LastModified: alioss.ToTime(out.LastModified),
	}, nil
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
	in := &alioss.ListObjectsV2Request{
		Bucket:  alioss.Ptr(s.bucket),
		MaxKeys: int32(limit),
	}
	if opts.Prefix != "" {
		in.Prefix = alioss.Ptr(opts.Prefix)
	}
	if opts.ContinuationToken != "" {
		in.ContinuationToken = alioss.Ptr(opts.ContinuationToken)
	}
	out, err := s.client.ListObjectsV2(ctx, in)
	if err != nil {
		return storage.ListResult{}, mapErr(err)
	}
	items := make([]storage.ObjectInfo, 0, len(out.Contents))
	for _, obj := range out.Contents {
		items = append(items, storage.ObjectInfo{
			Key:          alioss.ToString(obj.Key),
			Size:         obj.Size,
			ETag:         trimETag(alioss.ToString(obj.ETag)),
			LastModified: alioss.ToTime(obj.LastModified),
		})
	}
	res := storage.ListResult{
		Items:       items,
		IsTruncated: out.IsTruncated,
	}
	if res.IsTruncated {
		res.NextToken = alioss.ToString(out.NextContinuationToken)
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
	out, err := s.client.Presign(ctx, &alioss.PutObjectRequest{
		Bucket:      alioss.Ptr(s.bucket),
		Key:         alioss.Ptr(key),
		ContentType: alioss.Ptr(ct),
		Metadata:    opts.Metadata,
	}, alioss.PresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return out.URL, nil
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
	out, err := s.client.Presign(ctx, &alioss.GetObjectRequest{
		Bucket: alioss.Ptr(s.bucket),
		Key:    alioss.Ptr(key),
	}, alioss.PresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

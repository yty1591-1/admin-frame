package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// LocalConfig 本地目录对象存储配置。
type LocalConfig struct {
	// Root 根目录（绝对或相对）；必须可创建。
	Root string `yaml:"root"`
	// MaxObjectBytes 单对象上限；0 用 DefaultMaxObjectBytes。
	MaxObjectBytes int64 `yaml:"max_object_bytes"`
}

// Local 基于本地文件系统的 Store（开发 / 单机）。
type Local struct {
	root   string
	max    int64
	closed atomic.Bool
}

// OpenLocal 创建根目录并返回 Local Store。
func OpenLocal(cfg LocalConfig) (*Local, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("%w: root is required", ErrInvalidConfig)
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("%w: mkdir root: %v", ErrInvalidConfig, err)
	}
	maxBytes := cfg.MaxObjectBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxObjectBytes
	}
	return &Local{root: root, max: maxBytes}, nil
}

// Put 实现 Store；先写临时文件再原子 rename。
func (l *Local) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (ObjectInfo, error) {
	if err := l.ready(ctx); err != nil {
		return ObjectInfo{}, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if body == nil {
		body = strings.NewReader("")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return ObjectInfo{}, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return ObjectInfo{}, err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	hr := newSHA256Reader(newLimitedReader(body, resolveMaxBytes(opts, l.max)))
	if _, err := io.Copy(tmp, hr); err != nil {
		return ObjectInfo{}, err
	}
	if err := tmp.Sync(); err != nil {
		return ObjectInfo{}, err
	}
	if err := tmp.Close(); err != nil {
		return ObjectInfo{}, err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return ObjectInfo{}, err
	}

	// 旁路元数据文件（可选简单 JSON 省略：ContentType/Metadata 写入同名 .meta —— 保持轻量：
	// ContentType 存在 sidecar，失败不阻断正文。
	_ = writeSidecar(full, opts)

	st, err := os.Stat(full)
	if err != nil {
		return ObjectInfo{}, err
	}
	ct, md := readSidecar(full)
	if ct == "" {
		ct = contentTypeOrDefault(opts.ContentType)
	}
	return ObjectInfo{
		Key:          key,
		Size:         st.Size(),
		ContentType:  ct,
		ETag:         hr.etag(),
		Metadata:     md,
		LastModified: st.ModTime().UTC(),
	}, nil
}

// Get 实现 Store。
func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	info, err := l.Stat(ctx, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := os.Open(full) //nolint:gosec // full 由 resolve 校验并限定在 root 内
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}
	return f, info, nil
}

// Delete 实现 Store。
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := l.ready(ctx); err != nil {
		return err
	}
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	_ = os.Remove(sidecarPath(full))
	return nil
}

// Stat 实现 Store。
func (l *Local) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := l.ready(ctx); err != nil {
		return ObjectInfo{}, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, err
	}
	if st.IsDir() {
		return ObjectInfo{}, ErrNotFound
	}
	ct, md := readSidecar(full)
	etag, _ := fileETag(full)
	return ObjectInfo{
		Key:          key,
		Size:         st.Size(),
		ContentType:  contentTypeOrDefault(ct),
		ETag:         etag,
		Metadata:     md,
		LastModified: st.ModTime().UTC(),
	}, nil
}

// Exists 实现 Store。
func (l *Local) Exists(ctx context.Context, key string) (bool, error) {
	_, err := l.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if err == ErrNotFound {
		return false, nil
	}
	return false, err
}

// List 实现 Store。
func (l *Local) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	if err := l.ready(ctx); err != nil {
		return ListResult{}, err
	}
	if opts.Prefix != "" && (strings.Contains(opts.Prefix, "..") || strings.HasPrefix(opts.Prefix, "/")) {
		return ListResult{}, ErrInvalidKey
	}
	limit := listLimit(opts.Limit)
	var keys []string
	err := filepath.WalkDir(l.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".meta") {
			return nil
		}
		rel, err := filepath.Rel(l.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if opts.Prefix != "" && !strings.HasPrefix(key, opts.Prefix) {
			return nil
		}
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	sort.Strings(keys)

	start := 0
	if opts.ContinuationToken != "" {
		for i, k := range keys {
			if k > opts.ContinuationToken {
				start = i
				break
			}
			if i == len(keys)-1 {
				start = len(keys)
			}
		}
	}

	items := make([]ObjectInfo, 0, limit)
	var next string
	truncated := false
	for i := start; i < len(keys); i++ {
		if len(items) >= limit {
			truncated = true
			next = items[len(items)-1].Key
			break
		}
		info, err := l.Stat(ctx, keys[i])
		if err != nil {
			continue
		}
		items = append(items, info)
	}
	return ListResult{Items: items, NextToken: next, IsTruncated: truncated}, nil
}

// Close 实现 Store。
func (l *Local) Close() error {
	if l != nil {
		l.closed.Store(true)
	}
	return nil
}

// PresignPut 本地后端不支持。
func (l *Local) PresignPut(context.Context, string, time.Duration, PutOptions) (string, error) {
	return "", ErrUnsupported
}

// PresignGet 本地后端不支持。
func (l *Local) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", ErrUnsupported
}

// Root 返回绝对根路径。
func (l *Local) Root() string {
	if l == nil {
		return ""
	}
	return l.root
}

func (l *Local) resolve(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	full := filepath.Join(l.root, filepath.FromSlash(key))
	full = filepath.Clean(full)
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return full, nil
}

func (l *Local) ready(ctx context.Context) error {
	if l == nil || l.closed.Load() {
		return ErrClosed
	}
	return ctx.Err()
}

func fileETag(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // 仅由本地后端以已校验的对象路径调用
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hr := newSHA256Reader(f)
	if _, err := io.Copy(io.Discard, hr); err != nil {
		return "", err
	}
	return hr.etag(), nil
}

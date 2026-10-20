package storage

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type memObject struct {
	info ObjectInfo
	data []byte
}

// MemoryConfig 配置内存存储。
type MemoryConfig struct {
	// MaxObjectBytes 单对象上限；0 用 DefaultMaxObjectBytes。
	MaxObjectBytes int64
}

// Memory 进程内对象存储（单测 / 演示）。
type Memory struct {
	mu      sync.RWMutex
	objects map[string]memObject
	max     int64
	closed  atomic.Bool
	now     func() time.Time
}

// NewMemory 构造空内存存储。
func NewMemory(cfg MemoryConfig) *Memory {
	maxBytes := cfg.MaxObjectBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxObjectBytes
	}
	return &Memory{
		objects: make(map[string]memObject),
		max:     maxBytes,
		now:     time.Now,
	}
}

// Put 实现 Store。
func (m *Memory) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (ObjectInfo, error) {
	if err := m.ready(ctx); err != nil {
		return ObjectInfo{}, err
	}
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	if body == nil {
		body = bytes.NewReader(nil)
	}
	hr := newSHA256Reader(newLimitedReader(body, resolveMaxBytes(opts, m.max)))
	data, err := io.ReadAll(hr)
	if err != nil {
		return ObjectInfo{}, err
	}
	info := ObjectInfo{
		Key:          key,
		Size:         int64(len(data)),
		ContentType:  contentTypeOrDefault(opts.ContentType),
		ETag:         hr.etag(),
		Metadata:     cloneMetadata(opts.Metadata),
		LastModified: m.now().UTC(),
	}
	m.mu.Lock()
	m.objects[key] = memObject{info: info, data: data}
	m.mu.Unlock()
	return info, nil
}

// Get 实现 Store。
func (m *Memory) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	info, data, err := m.load(ctx, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return io.NopCloser(bytes.NewReader(data)), info, nil
}

// Delete 实现 Store。
func (m *Memory) Delete(ctx context.Context, key string) error {
	if err := m.ready(ctx); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; !ok {
		return ErrNotFound
	}
	delete(m.objects, key)
	return nil
}

// Stat 实现 Store。
func (m *Memory) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, _, err := m.load(ctx, key)
	return info, err
}

// Exists 实现 Store。
func (m *Memory) Exists(ctx context.Context, key string) (bool, error) {
	_, err := m.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if err == ErrNotFound {
		return false, nil
	}
	return false, err
}

// List 实现 Store；ContinuationToken 为上一页最后一个 Key。
func (m *Memory) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	if err := m.ready(ctx); err != nil {
		return ListResult{}, err
	}
	if opts.Prefix != "" {
		if err := ValidateKey(opts.Prefix); err != nil {
			// 允许前缀以未完整段结束？前缀可不完整，仅禁穿越
			if strings.Contains(opts.Prefix, "..") || strings.HasPrefix(opts.Prefix, "/") {
				return ListResult{}, ErrInvalidKey
			}
		}
	}
	limit := listLimit(opts.Limit)
	m.mu.RLock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		if opts.Prefix == "" || strings.HasPrefix(k, opts.Prefix) {
			keys = append(keys, k)
		}
	}
	m.mu.RUnlock()
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
	m.mu.RLock()
	for i := start; i < len(keys); i++ {
		if len(items) >= limit {
			truncated = true
			next = items[len(items)-1].Key
			break
		}
		obj := m.objects[keys[i]]
		info := obj.info
		info.Metadata = cloneMetadata(info.Metadata)
		items = append(items, info)
	}
	m.mu.RUnlock()
	return ListResult{Items: items, NextToken: next, IsTruncated: truncated}, nil
}

// Close 实现 Store。
func (m *Memory) Close() error {
	if m == nil {
		return nil
	}
	m.closed.Store(true)
	m.mu.Lock()
	m.objects = nil
	m.mu.Unlock()
	return nil
}

// PresignPut 内存后端不支持。
func (m *Memory) PresignPut(context.Context, string, time.Duration, PutOptions) (string, error) {
	return "", ErrUnsupported
}

// PresignGet 内存后端不支持。
func (m *Memory) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", ErrUnsupported
}

func (m *Memory) load(ctx context.Context, key string) (ObjectInfo, []byte, error) {
	if err := m.ready(ctx); err != nil {
		return ObjectInfo{}, nil, err
	}
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[key]
	if !ok {
		return ObjectInfo{}, nil, ErrNotFound
	}
	info := obj.info
	info.Metadata = cloneMetadata(info.Metadata)
	data := append([]byte(nil), obj.data...)
	return info, data, nil
}

func (m *Memory) ready(ctx context.Context) error {
	if m == nil || m.closed.Load() {
		return ErrClosed
	}
	return ctx.Err()
}

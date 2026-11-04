package persistence

import (
	"context"
	"sync"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
)

// MemoryDLQReader 是死信载荷读取的内存实现（单测 / 本地开发）。
type MemoryDLQReader struct {
	mu       sync.Mutex
	payloads map[string][]byte // source_event_id -> 载荷
}

// NewMemoryDLQReader 构造内存 DLQ 读取器。
func NewMemoryDLQReader() *MemoryDLQReader {
	return &MemoryDLQReader{payloads: make(map[string][]byte)}
}

// Put 写入一条死信载荷（测试辅助，模拟 Outbox MarkDead 的落库结果）。
func (r *MemoryDLQReader) Put(sourceEventID string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payloads[sourceEventID] = payload
}

// PayloadBySource 按原事件 ID 读取载荷。
func (r *MemoryDLQReader) PayloadBySource(_ context.Context, sourceEventID string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, ok := r.payloads[sourceEventID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return raw, nil
}

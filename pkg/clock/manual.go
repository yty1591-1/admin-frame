package clock

import (
	"sync"
	"time"
)

// Manual 是可手动拨动的时钟，供测试确定性推进时间；并发安全。
//
// 时间只在本对象内推进，不影响 time.Now；因此不能用于需要真实等待的集成测试。
// 含互斥锁，不得在首次使用后复制。
type Manual struct {
	mu  sync.RWMutex
	now time.Time
}

// NewManual 创建手动时钟；t 为零值时使用 Unix epoch（UTC），便于构造稳定基准。
func NewManual(t time.Time) *Manual {
	if t.IsZero() {
		t = time.Unix(0, 0).UTC()
	}
	return &Manual{now: t}
}

// Now 返回当前模拟时间；nil 接收者返回零时间。
func (m *Manual) Now() time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.now
}

// Set 将时钟设为绝对时间 t；nil 接收者为空操作。
func (m *Manual) Set(t time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = t
}

// Advance 将时钟前移 d；d 为负时等价于回拨，调用方应避免依赖回拨语义。
func (m *Manual) Advance(d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}

// 编译期断言。
var _ Clock = (*Manual)(nil)

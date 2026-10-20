package email

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Opener 由各驱动包在 init 中注册：根据统一 Config 构造 Sender。
//
// 传入的 Config 已完成 Normalized；驱动仍应校验自身字段并返回 ErrInvalidConfig。
type Opener func(ctx context.Context, cfg Config) (Sender, error)

var (
	driversMu sync.RWMutex
	drivers   = map[string]Opener{}
)

// Register 注册驱动名（大小写不敏感）。同名后注册覆盖先注册，仅测试使用。
func Register(name string, open Opener) {
	name = normalizeDriver(name)
	if name == "" || open == nil {
		return
	}
	driversMu.Lock()
	drivers[name] = open
	driversMu.Unlock()
}

// Unregister 移除驱动，仅测试使用。
func Unregister(name string) {
	driversMu.Lock()
	delete(drivers, normalizeDriver(name))
	driversMu.Unlock()
}

// Open 按 cfg.Driver 构造 Sender；未知驱动返回 ErrUnknownDriver。
//
// 驱动须在进程装配处 blank-import，例如：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/email/smtp"
func Open(ctx context.Context, cfg Config) (Sender, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	open, err := lookup(cfg.Driver)
	if err != nil {
		return nil, err
	}
	return open(ctx, cfg)
}

// Drivers 返回已注册驱动名快照，按字典序排序。
func Drivers() []string {
	driversMu.RLock()
	out := make([]string, 0, len(drivers))
	for name := range drivers {
		out = append(out, name)
	}
	driversMu.RUnlock()
	sort.Strings(out)
	return out
}

func lookup(name string) (Opener, error) {
	normalized := normalizeDriver(name)
	driversMu.RLock()
	open, ok := drivers[normalized]
	driversMu.RUnlock()
	if !ok || open == nil {
		return nil, fmt.Errorf("%w: %q (blank-import the driver package?)", ErrUnknownDriver, normalized)
	}
	return open, nil
}

func normalizeDriver(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

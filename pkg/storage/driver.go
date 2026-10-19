package storage

import (
	"context"
	"fmt"
	"sync"
)

// Opener 由各驱动包在 init 中注册：根据统一 Config 构造 Store。
type Opener func(ctx context.Context, cfg Config) (Store, error)

var (
	driversMu sync.RWMutex
	drivers   = map[string]Opener{}
)

// Register 注册驱动名（小写）。重复注册同名将覆盖并可用于测试替换。
func Register(name string, open Opener) {
	name = normalizeDriver(name)
	if name == "" || open == nil {
		return
	}
	driversMu.Lock()
	drivers[name] = open
	driversMu.Unlock()
}

// Unregister 移除驱动（测试用）。
func Unregister(name string) {
	driversMu.Lock()
	delete(drivers, normalizeDriver(name))
	driversMu.Unlock()
}

// Drivers 返回已注册驱动名快照（排序不稳定，调用方可自行 sort）。
func Drivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	out := make([]string, 0, len(drivers))
	for k := range drivers {
		out = append(out, k)
	}
	return out
}

func lookup(name string) (Opener, error) {
	name = normalizeDriver(name)
	driversMu.RLock()
	open, ok := drivers[name]
	driversMu.RUnlock()
	if !ok || open == nil {
		return nil, fmt.Errorf("%w: driver %q (blank-import the driver package?)", ErrUnknownDriver, name)
	}
	return open, nil
}

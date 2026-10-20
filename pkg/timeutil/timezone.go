package timeutil

import (
	"fmt"
	"sync"
	"time"
)

// locationCache 缓存已加载的时区，避免热路径重复读取 zoneinfo。
//
// 时区数据库在进程生命周期内视为只读；运行期修改系统 TZ 不会刷新缓存。
var locationCache sync.Map // map[string]*time.Location

// Location 按 IANA 名称加载时区。
//
// name 为空或 "UTC" 时返回 time.UTC；"Local" 返回 time.Local。
// 加载结果按名称缓存；无效名称返回包装后的错误。
func Location(name string) (*time.Location, error) {
	switch name {
	case "", "UTC", "Etc/UTC":
		return time.UTC, nil
	case "Local":
		return time.Local, nil
	}
	if v, ok := locationCache.Load(name); ok {
		loc, _ := v.(*time.Location)
		return loc, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timeutil: load location %q: %w", name, err)
	}
	actual, _ := locationCache.LoadOrStore(name, loc)
	loc, _ = actual.(*time.Location)
	return loc, nil
}

// MustLocation 同 Location，加载失败时 panic；仅用于启动期常量配置。
func MustLocation(name string) *time.Location {
	loc, err := Location(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// NormalizeLocation 将 nil 时区归一为 UTC。
func NormalizeLocation(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// InLocation 将 t 转换到 loc；loc 为 nil 时按 UTC。
func InLocation(t time.Time, loc *time.Location) time.Time {
	return t.In(NormalizeLocation(loc))
}

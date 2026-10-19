package pagination

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/errors"
)

// Direction 排序方向。
type Direction string

const (
	// Asc 升序。
	Asc Direction = "asc"
	// Desc 降序。
	Desc Direction = "desc"
)

// Order 单个排序项（Name 为白名单映射后的列名/字段名）。
type Order struct {
	Name      string
	Direction Direction
}

// Descending 报告是否降序。
func (o Order) Descending() bool { return o.Direction == Desc }

// SortConfig 排序解析配置。
type SortConfig struct {
	// Allowed 客户端字段 → 内部列名；未出现的字段一律拒绝。
	// 例：{"created_at":"created_at","amount":"amount_cent"}
	Allowed map[string]string
	// Default 未传 sort 时的默认排序；可为 nil。
	Default []Order
	// Param 查询键；空则 "sort"。
	Param string
	// MaxFields 最多接受的排序字段数；≤0 表示 3。
	MaxFields int
}

func (c SortConfig) normalize() SortConfig {
	if c.Param == "" {
		c.Param = defaultSortParam
	}
	if c.MaxFields <= 0 {
		c.MaxFields = 3
	}
	return c
}

// ParseSort 解析 sort 查询参数。
//
// 格式：逗号分隔字段，前缀 "-" 表示降序，如 created_at,-amount。
// 无参数时返回 Default 副本；Allowed 为空时拒绝任何非空 sort。
func ParseSort(r *http.Request, cfg SortConfig) ([]Order, error) {
	if r == nil {
		return nil, errors.InvalidArgument("request is nil")
	}
	cfg = cfg.normalize()
	raw := strings.TrimSpace(r.URL.Query().Get(cfg.Param))
	if raw == "" {
		return cloneOrders(cfg.Default), nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > cfg.MaxFields {
		return nil, errors.InvalidArgument(
			fmt.Sprintf("sort exceeds maximum of %d fields", cfg.MaxFields),
		)
	}
	if len(cfg.Allowed) == 0 {
		return nil, errors.InvalidArgument("sort is not allowed")
	}
	out := make([]Order, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		dir := Asc
		name := p
		if strings.HasPrefix(p, "-") {
			dir = Desc
			name = strings.TrimPrefix(p, "-")
		} else if strings.HasPrefix(p, "+") {
			name = strings.TrimPrefix(p, "+")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.InvalidArgument("invalid sort field")
		}
		col, ok := cfg.Allowed[name]
		if !ok || col == "" {
			return nil, errors.InvalidArgument(fmt.Sprintf("unsupported sort field %q", name))
		}
		if _, dup := seen[col]; dup {
			continue
		}
		seen[col] = struct{}{}
		out = append(out, Order{Name: col, Direction: dir})
	}
	if len(out) == 0 {
		return cloneOrders(cfg.Default), nil
	}
	return out, nil
}

// SQLOrderClause 将排序列表格式化为 "col ASC, col2 DESC"（仅用于已白名单列名）。
// 空列表返回空串。
func SQLOrderClause(orders []Order) string {
	if len(orders) == 0 {
		return ""
	}
	parts := make([]string, 0, len(orders))
	for _, o := range orders {
		dir := "ASC"
		if o.Descending() {
			dir = "DESC"
		}
		parts = append(parts, o.Name+" "+dir)
	}
	return strings.Join(parts, ", ")
}

func cloneOrders(in []Order) []Order {
	if len(in) == 0 {
		return nil
	}
	out := make([]Order, len(in))
	copy(out, in)
	return out
}

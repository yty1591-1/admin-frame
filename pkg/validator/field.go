package validator

import (
	"reflect"
	"strings"
)

// fieldName 优先 json tag（忽略 omitempty 等选项），否则用结构体字段名。
func fieldName(fld reflect.StructField) string {
	if j := fld.Tag.Get("json"); j != "" && j != "-" {
		name, _, _ := strings.Cut(j, ",")
		if name != "" && name != "-" {
			return name
		}
	}
	if f := fld.Tag.Get("form"); f != "" && f != "-" {
		name, _, _ := strings.Cut(f, ",")
		if name != "" {
			return name
		}
	}
	return fld.Name
}

func namespacePath(ns string) string {
	// go-playground 命名空间形如 CreateOrderRequest.AmountCent
	if i := strings.IndexByte(ns, '.'); i >= 0 && i+1 < len(ns) {
		return ns[i+1:]
	}
	return ns
}

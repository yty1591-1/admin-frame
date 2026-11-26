package appconfig_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// TestBusinessSpecDefaultsMatchCode 校验业务域默认值与代码 ApplyDefaults /
// Normalize 的生效值一致，避免首次播种写入错误基线。
func TestBusinessSpecDefaultsMatchCode(t *testing.T) {
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("platform registry: %v", err)
	}
	cfg := appconfig.Config{}
	cfg.ApplyDefaults()
	cfg.IAM.Normalize()

	for _, namespace := range []string{
		spec.NamespaceIdentity, spec.NamespaceIAM, spec.NamespaceAudit,
		spec.NamespaceTenant, spec.NamespaceNotification,
	} {
		for _, keySpec := range registry.List(namespace) {
			if keySpec.Default == "" {
				continue // 无默认值：不参与播种
			}
			got, ok := defaultByYAMLPath(&cfg, keySpec.Key)
			if !ok {
				t.Errorf("declared key %q has no matching config field", keySpec.Key)
				continue
			}
			if !sameDefault(keySpec, got) {
				t.Errorf("default of %q = %q, want %q", keySpec.Key, got, keySpec.Default)
			}
		}
	}
}

// defaultByYAMLPath 按 yaml 路径读取配置默认值，返回字符串形式。
func defaultByYAMLPath(cfg any, path string) (string, bool) {
	value := reflect.ValueOf(cfg).Elem()
	for _, segment := range strings.Split(path, ".") {
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return "", false
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return "", false
		}
		index, ok := yamlFieldIndex(value.Type(), segment)
		if !ok {
			return "", false
		}
		value = value.FieldByIndex(index)
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if value.Type() == reflect.TypeOf(time.Duration(0)) {
		return time.Duration(value.Int()).String(), true
	}
	switch value.Kind() {
	case reflect.String:
		return value.String(), true
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'f', -1, 64), true
	case reflect.Slice, reflect.Map:
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return "", false
		}
		return string(encoded), true
	default:
		return "", false
	}
}

// yamlFieldIndex 查找 yaml 段对应的字段索引路径，支持 inline 嵌入结构体。
func yamlFieldIndex(t reflect.Type, segment string) ([]int, bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("yaml")
		name, options, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == segment && !strings.Contains(options, "inline") {
			return []int{i}, true
		}
		if strings.Contains(options, "inline") || (field.Anonymous && name == "") {
			inner := field.Type
			for inner.Kind() == reflect.Pointer {
				inner = inner.Elem()
			}
			if inner.Kind() != reflect.Struct {
				continue
			}
			if index, ok := yamlFieldIndex(inner, segment); ok {
				return append([]int{i}, index...), true
			}
		}
	}
	return nil, false
}

// sameDefault 按声明类型比较默认值，避免等价时长 / 整数的字符串差异。
func sameDefault(keySpec domain.KeySpec, got string) bool {
	switch keySpec.ValueType {
	case domain.ValueTypeDuration:
		want, wantErr := time.ParseDuration(keySpec.Default)
		actual, actualErr := time.ParseDuration(got)
		return wantErr == nil && actualErr == nil && want == actual
	case domain.ValueTypeInt:
		want, wantErr := strconv.ParseInt(keySpec.Default, 10, 64)
		actual, actualErr := strconv.ParseInt(got, 10, 64)
		return wantErr == nil && actualErr == nil && want == actual
	case domain.ValueTypeBool:
		want, wantErr := strconv.ParseBool(keySpec.Default)
		actual, actualErr := strconv.ParseBool(got)
		return wantErr == nil && actualErr == nil && want == actual
	default:
		return keySpec.Default == got
	}
}

package config

import (
	"reflect"
	"strings"
)

// BindValues 把配置中心快照的扁平取值写入 target。
//
// 键为 yaml 路径（小写、点分隔），如 server.addr、http.read_timeout；
// 命名规则与 BindEnv 一致，但使用 yaml 同名小写路径而非大写环境变量名：
//   - 字段路径由 yaml 标签优先、否则由字段名推导为小写蛇形，逐段以 "." 连接
//   - 匿名嵌入结构体默认内联，不增加路径段
//   - `yaml:"-"` 跳过该字段
//
// 取值规则：
//   - 快照中不存在的键不会覆盖文件/环境变量值；空串是合法取值，会被写入
//   - 快照中出现但 target 未声明的键静默忽略（一份快照可能覆盖多个 namespace）
//   - 类型解析失败返回 ErrInvalidConfig + ErrSnapshotOverride，错误文本不含取值
//
// 调用方负责保证 target 已完成默认值与校验；本函数只做覆盖写入。
func BindValues(target any, values map[string]string) error {
	if target == nil {
		return invalidConfig("snapshot target must not be nil")
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return invalidConfig("snapshot target must be a non-nil pointer to struct")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return invalidConfig("snapshot target must point to struct, got %s", value.Kind())
	}
	if len(values) == 0 {
		return nil
	}
	binder := &valueBinder{values: values}
	_, err := binder.walk(value, nil, nil)
	return err
}

// applyValueAliases 把配置中心键按别名补充为 yaml 路径；不修改入参。
func applyValueAliases(values map[string]string, aliases map[string]string) map[string]string {
	if len(aliases) == 0 || len(values) == 0 {
		return values
	}
	copied := make(map[string]string, len(values)+len(aliases))
	for key, value := range values {
		copied[key] = value
	}
	for key, path := range aliases {
		if value, ok := values[key]; ok {
			copied[path] = value
		}
	}
	return copied
}

// valueBinder 承载一次快照覆盖所需的不可变状态。
type valueBinder struct {
	values map[string]string
}

// walk 递归绑定结构体字段；返回值表示是否有字段被快照覆盖。
func (b *valueBinder) walk(value reflect.Value, keyPath, fieldPath []string) (applied bool, err error) {
	structType := value.Type()
	for i := 0; i < structType.NumField(); i++ {
		structField := structType.Field(i)
		if structField.PkgPath != "" && !inlineUnexported(structField) {
			continue
		}
		if _, skip := valueSkip(structField); skip {
			continue
		}
		fieldValue := value.Field(i)
		currentPath := appendSegment(fieldPath, structField.Name)

		if isBranch(structField.Type) {
			nextKeyPath := keyPath
			if !inlineField(structField) {
				nextKeyPath = appendSegment(keyPath, valueSegment(structField))
			}
			hit, walkErr := b.walkBranch(fieldValue, nextKeyPath, currentPath)
			if walkErr != nil {
				return false, walkErr
			}
			applied = applied || hit
			continue
		}

		if !leafSupported(structField.Type) {
			continue
		}
		key := strings.Join(appendSegment(keyPath, valueSegment(structField)), ".")
		raw, ok := b.values[key]
		if !ok {
			continue
		}
		if reason := assignLeaf(fieldValue, raw); reason != "" {
			if reason == reasonUnsupportedLeaf {
				continue
			}
			return false, snapshotError(key, reason)
		}
		applied = true
	}
	return applied, nil
}

// walkBranch 进入嵌套结构体；指针分支仅在确有覆盖时才分配。
func (b *valueBinder) walkBranch(field reflect.Value, keyPath, fieldPath []string) (applied bool, err error) {
	if field.Kind() != reflect.Pointer {
		return b.walk(field, keyPath, fieldPath)
	}
	if field.Type().Elem().Kind() == reflect.Pointer {
		return false, invalidConfig("field %s: multi-level pointers are not supported", strings.Join(fieldPath, "."))
	}
	nested := reflect.New(field.Type().Elem()).Elem()
	if !field.IsNil() {
		nested.Set(field.Elem())
	}
	applied, err = b.walk(nested, keyPath, fieldPath)
	if err != nil || !applied {
		return applied, err
	}
	field.Set(nested.Addr())
	return true, nil
}

// valueSkip 读取 yaml 跳过标记。
func valueSkip(field reflect.StructField) (name string, skip bool) {
	tag, ok := field.Tag.Lookup(yamlTag)
	if !ok {
		return "", false
	}
	name, _, _ = strings.Cut(tag, ",")
	return name, name == envTagSkip
}

// valueSegment 推导字段在快照键中的路径段：yaml 名称优先，否则小写蛇形。
func valueSegment(field reflect.StructField) string {
	if name, ok := valueSkip(field); ok {
		return ""
	} else if name != "" {
		return normalizeValueSegment(name)
	}
	return strings.ToLower(snakeCase(field.Name))
}

// normalizeValueSegment 把 yaml 名称规范化为快照键段：小写、短横线转下划线。
func normalizeValueSegment(segment string) string {
	lowered := strings.ToLower(strings.TrimSpace(segment))
	return strings.ReplaceAll(lowered, "-", "_")
}

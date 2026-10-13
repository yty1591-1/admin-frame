package errors

import "fmt"

// Field 是附着在错误上的结构化键值，供日志与观测使用。
// 不得放入密钥、完整 Token、银行卡等敏感信息。
type Field struct {
	Key   string
	Value any
}

// F 构造一个 Field。
func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// String 返回 key=value 形式的调试文本。
func (f Field) String() string {
	return fmt.Sprintf("%s=%v", f.Key, f.Value)
}

// FieldsMap 将字段转为 map（后写覆盖先写同名键）。
func FieldsMap(fields []Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		m[f.Key] = f.Value
	}
	return m
}

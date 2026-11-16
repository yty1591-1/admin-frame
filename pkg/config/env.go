package config

import (
	"encoding"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	envTag  = "env"
	yamlTag = "yaml"

	envTagSkip = "-"

	// maxEnvKeyBytes 限制派生出的环境变量名长度，避免异常结构体制造超长键。
	maxEnvKeyBytes = 128

	// reasonUnsupportedLeaf 表示叶子类型不支持字符串绑定。
	reasonUnsupportedLeaf = "unsupported leaf type"
)

var (
	durationType    = reflect.TypeOf(time.Duration(0))
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// BindEnv 按结构体标签把环境变量写入 target（无路径白名单）。
//
// 业务进程应使用 Options.EnvPathAllowlist 或 BindEnvFiltered，仅放行 db/redis。
//
// 命名规则（以 EnvPrefix="APP" 为例）：
//   - 字段路径由 yaml 标签优先、否则由字段名的 SNAKE_CASE 推导，
//     逐段以 "_" 连接并加上前缀：Log.Level → APP_LOG_LEVEL
//   - `yaml:"dsn"` → APP_DB_DSN（嵌套结构逐段拼接）
//   - 匿名嵌入结构体默认内联，不增加路径段
//   - `env:"CUSTOM_NAME"` 使用完整变量名，忽略 EnvPrefix 与路径推导
//   - `env:"-"` 跳过该字段
//
// 取值规则：
//   - 变量不存在或去除空白后为空时视为「未设置」，不会覆盖文件值；
//     因此无法通过环境变量把字段置为空串
//   - 支持 string、bool、全部有/无符号整数、float32/64、time.Duration、
//     []string（逗号分隔）、上述类型的单层指针，以及 encoding.TextUnmarshaler
//   - 不支持的类型在未加 env 标签时静默跳过（自动推导只覆盖标量）；
//     若显式标注 env 标签则返回 ErrUnsupportedField，避免写完不生效
//
// lookup 为 nil 时使用 os.LookupEnv。返回错误均为 ErrInvalidConfig +
// ErrEnvOverride 的子集；错误文本不包含变量取值。
func BindEnv(target any, prefix string, lookup func(key string) (value string, ok bool)) error {
	return BindEnvFiltered(target, prefix, nil, lookup)
}

// BindEnvFiltered 与 BindEnv 相同，但仅绑定 yaml 根段落在 allowPaths 内的字段。
//
// allowPaths 为空表示不限制；非空时路径段经 normalizeSegment 后与字段 yaml
// 根段比较（如 "db"/"redis" → DB/REDIS）。显式 `env:"NAME"` 叶子若位于
// 未放行分支则跳过，避免业务密钥经绝对变量名旁路。
func BindEnvFiltered(target any, prefix string, allowPaths []string,
	lookup func(key string) (value string, ok bool)) error {
	if target == nil {
		return invalidConfig("environment target must not be nil")
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return invalidConfig("environment target must be a non-nil pointer to struct")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return invalidConfig("environment target must point to struct, got %s", value.Kind())
	}
	normalized, err := normalizePrefix(prefix)
	if err != nil {
		return err
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	allowRoots, err := normalizeAllowPaths(allowPaths)
	if err != nil {
		return err
	}
	binder := &envBinder{prefix: normalized, lookup: lookup, allowRoots: allowRoots}
	if _, err := binder.walk(value, nil, nil); err != nil {
		return err
	}
	return nil
}

// envBinder 承载一次绑定所需的不可变状态。
type envBinder struct {
	prefix     string
	lookup     func(key string) (value string, ok bool)
	allowRoots map[string]struct{}
}

// walk 递归绑定结构体字段；返回值表示是否有字段被环境变量覆盖。
func (b *envBinder) walk(value reflect.Value, envPath, fieldPath []string) (applied bool, err error) {
	structType := value.Type()
	for i := 0; i < structType.NumField(); i++ {
		structField := structType.Field(i)
		if structField.PkgPath != "" && !inlineUnexported(structField) {
			continue
		}
		explicit, skip := envOverride(structField)
		if skip {
			continue
		}
		fieldValue := value.Field(i)
		currentPath := appendSegment(fieldPath, structField.Name)

		if isBranch(structField.Type) {
			if explicit != "" {
				return false, invalidConfig("field %s: env tag on nested struct is not supported; rename with a yaml tag",
					strings.Join(currentPath, "."))
			}
			nextEnvPath := envPath
			if !inlineField(structField) {
				nextEnvPath = appendSegment(envPath, fieldSegment(structField))
			}
			// 内联嵌入尚未形成 yaml 根段，不能按空路径拒绝，否则会跳过整棵平台段。
			if len(nextEnvPath) > 0 && !b.rootAllowed(nextEnvPath) {
				continue
			}
			hit, walkErr := b.walkBranch(fieldValue, nextEnvPath, currentPath)
			if walkErr != nil {
				return false, walkErr
			}
			applied = applied || hit
			continue
		}

		leafPath := appendSegment(envPath, fieldSegment(structField))
		if !b.rootAllowed(leafPath) {
			continue
		}
		key := b.keyFor(structField, explicit, envPath)
		if err := validateEnvKey(key, strings.Join(currentPath, ".")); err != nil {
			return false, err
		}
		if !leafSupported(structField.Type) {
			if explicit != "" {
				return false, unsupportedFieldError(strings.Join(currentPath, "."), key)
			}
			continue
		}
		hit, bindErr := b.applyLeaf(fieldValue, key)
		if bindErr != nil {
			return false, bindErr
		}
		applied = applied || hit
	}
	return applied, nil
}

// rootAllowed 报告 envPath 的 yaml 根段是否在白名单内；无白名单时一律放行。
func (b *envBinder) rootAllowed(envPath []string) bool {
	if len(b.allowRoots) == 0 {
		return true
	}
	if len(envPath) == 0 {
		return false
	}
	_, ok := b.allowRoots[envPath[0]]
	return ok
}

// normalizeAllowPaths 把调用方路径段规范为大写根段集合。
func normalizeAllowPaths(paths []string) (map[string]struct{}, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return nil, invalidConfig("env path allowlist entry must not be empty")
		}
		// 只取首段：调用方写 "db" 或 "db.dsn" 均按根段 db 放行整棵子树。
		root, _, _ := strings.Cut(trimmed, ".")
		segment := normalizeSegment(root)
		if segment == "" {
			return nil, invalidConfig("env path allowlist entry %q has empty root", path)
		}
		out[segment] = struct{}{}
	}
	return out, nil
}

// walkBranch 进入嵌套结构体；指针分支仅在确有覆盖时才分配，保持零值语义。
func (b *envBinder) walkBranch(field reflect.Value, envPath, fieldPath []string) (applied bool, err error) {
	if field.Kind() != reflect.Pointer {
		return b.walk(field, envPath, fieldPath)
	}
	if field.Type().Elem().Kind() == reflect.Pointer {
		return false, invalidConfig("field %s: multi-level pointers are not supported", strings.Join(fieldPath, "."))
	}
	nested := reflect.New(field.Type().Elem()).Elem()
	if !field.IsNil() {
		nested.Set(field.Elem())
	}
	applied, err = b.walk(nested, envPath, fieldPath)
	if err != nil || !applied {
		return applied, err
	}
	field.Set(nested.Addr())
	return true, nil
}

// applyLeaf 查询并写入单个字段；返回是否命中。
func (b *envBinder) applyLeaf(field reflect.Value, key string) (applied bool, err error) {
	raw, ok := b.lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return false, nil
	}
	if err := b.setLeaf(field, key, raw); err != nil {
		return false, err
	}
	return true, nil
}

// setLeaf 按目标类型解析 raw；错误只报告变量名与期望类型。
func (b *envBinder) setLeaf(field reflect.Value, key, raw string) error {
	reason := assignLeaf(field, raw)
	switch reason {
	case "":
		return nil
	case reasonUnsupportedLeaf:
		return unsupportedFieldError(field.Type().String(), key)
	default:
		return envError(key, reason)
	}
}

// assignLeaf 把字符串写入叶子字段，返回失败原因；空串表示成功。
//
// 原因文本不包含原始取值，供环境变量与配置中心快照共用。
func assignLeaf(field reflect.Value, raw string) string {
	if field.Type() == durationType {
		parsed, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return "expected a Go duration such as 5s or 1m30s"
		}
		field.SetInt(int64(parsed))
		return ""
	}
	if field.Kind() == reflect.Pointer {
		elem := reflect.New(field.Type().Elem())
		if reason := assignLeaf(elem.Elem(), raw); reason != "" {
			return reason
		}
		field.Set(elem)
		return ""
	}
	if field.CanAddr() {
		if unmarshaler, ok := field.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := unmarshaler.UnmarshalText([]byte(strings.TrimSpace(raw))); err != nil {
				return "expected text accepted by " + field.Type().String()
			}
			return ""
		}
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
		return ""
	case reflect.Bool:
		parsed, ok := parseBool(raw)
		if !ok {
			return "expected boolean: true/false, 1/0, yes/no or on/off"
		}
		field.SetBool(parsed)
		return ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, field.Type().Bits())
		if err != nil {
			return "expected a base-10 integer"
		}
		field.SetInt(parsed)
		return ""
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(strings.TrimSpace(raw), 10, field.Type().Bits())
		if err != nil {
			return "expected a non-negative base-10 integer"
		}
		field.SetUint(parsed)
		return ""
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), field.Type().Bits())
		if err != nil {
			return "expected a floating-point number"
		}
		field.SetFloat(parsed)
		return ""
	case reflect.Slice:
		parts := strings.Split(raw, ",")
		items := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				items = append(items, trimmed)
			}
		}
		field.Set(reflect.ValueOf(items).Convert(field.Type()))
		return ""
	default:
		return reasonUnsupportedLeaf
	}
}

// keyFor 计算环境变量名；显式 env 标签为完整变量名。
func (b *envBinder) keyFor(field reflect.StructField, explicit string, envPath []string) string {
	if explicit != "" {
		return explicit
	}
	segments := appendSegment(envPath, fieldSegment(field))
	if b.prefix == "" {
		return strings.Join(segments, "_")
	}
	return b.prefix + "_" + strings.Join(segments, "_")
}

// envOverride 读取 env 标签；第二个返回值为 true 表示显式跳过。
func envOverride(field reflect.StructField) (name string, skip bool) {
	tag, ok := field.Tag.Lookup(envTag)
	if !ok {
		return "", false
	}
	name, _, _ = strings.Cut(tag, ",")
	name = strings.TrimSpace(name)
	switch name {
	case "":
		return "", false
	case envTagSkip:
		return "", true
	default:
		return name, false
	}
}

// isBranch 报告字段是否为需要递归的嵌套结构体。
//
// 实现了 encoding.TextUnmarshaler 的结构体（如 time.Time）视为叶子。
func isBranch(t reflect.Type) bool {
	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return false
	}
	return !reflect.PointerTo(base).Implements(textUnmarshaler)
}

// leafSupported 报告叶子类型是否可自动绑定；不支持时自动推导会跳过。
func leafSupported(t reflect.Type) bool {
	if t == durationType {
		return true
	}
	if t.Kind() == reflect.Pointer {
		return leafSupported(t.Elem())
	}
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.String
	default:
		return false
	}
}

// inlineUnexported 报告未导出字段是否可安全参与绑定。
//
// 未导出匿名结构体本身不可写，但其导出子字段仍可寻址写入（与 encoding/json
// 一致），因此允许递归；匿名指针与普通未导出字段必须跳过，避免 reflect panic。
func inlineUnexported(field reflect.StructField) bool {
	if !field.Anonymous || field.Type.Kind() != reflect.Struct {
		return false
	}
	return isBranch(field.Type)
}

// inlineField 报告嵌套结构体是否应内联（匿名且无显式 yaml 名称）。
func inlineField(field reflect.StructField) bool {
	if !field.Anonymous {
		return false
	}
	tag, ok := field.Tag.Lookup(yamlTag)
	if !ok {
		return true
	}
	name, _, _ := strings.Cut(tag, ",")
	return name == ""
}

// fieldSegment 推导字段在环境变量名中的路径段。
func fieldSegment(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup(yamlTag); ok && tag != envTagSkip {
		name, _, _ := strings.Cut(tag, ",")
		if name != "" {
			return normalizeSegment(name)
		}
	}
	return snakeCase(field.Name)
}

// normalizeSegment 将 yaml 名称转为环境变量段：大写并把分隔符统一为 "_"。
func normalizeSegment(segment string) string {
	var builder strings.Builder
	builder.Grow(len(segment))
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch {
		case c >= 'a' && c <= 'z':
			builder.WriteByte(c - 'a' + 'A')
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			builder.WriteByte(c)
		case c == '-' || c == '.':
			builder.WriteByte('_')
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// snakeCase 将 Go 字段名转为 SNAKE_CASE，保留缩写与数字边界。
//
// 例：ShutdownTimeout → SHUTDOWN_TIMEOUT、DB → DB、S3Bucket → S3_BUCKET。
func snakeCase(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	builder.Grow(len(runes) * 2)
	for i, r := range runes {
		if !unicode.IsUpper(r) {
			builder.WriteRune(unicode.ToUpper(r))
			continue
		}
		if i > 0 && needsSeparator(runes, i) {
			builder.WriteByte('_')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// needsSeparator 判断大写字母前是否应插入分隔符。
func needsSeparator(runes []rune, i int) bool {
	previous := runes[i-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
}

// normalizePrefix 校验并规范化环境变量前缀；空前缀表示不加前缀。
func normalizePrefix(prefix string) (string, error) {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return "", nil
	}
	normalized := normalizeSegment(trimmed)
	trimmedNormalized := strings.Trim(normalized, "_")
	if trimmedNormalized == "" {
		return "", invalidConfig("env prefix must contain at least one letter or digit")
	}
	if len(trimmedNormalized) > maxEnvKeyBytes {
		return "", invalidConfig("env prefix exceeds %d bytes", maxEnvKeyBytes)
	}
	return trimmedNormalized, nil
}

// parseBool 接受 Go、shell 与常见配置写法。
func parseBool(raw string) (parsed, ok bool) {
	value := strings.TrimSpace(raw)
	if result, err := strconv.ParseBool(value); err == nil {
		return result, true
	}
	switch strings.ToLower(value) {
	case "yes", "on":
		return true, true
	case "no", "off":
		return false, true
	default:
		return false, false
	}
}

// appendSegment 返回追加一段后的新切片，避免调用方共享底层数组。
func appendSegment(path []string, segment string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = segment
	return out
}

// validateEnvKey 检查派生键长度，超限时返回可定位错误。
func validateEnvKey(key, fieldPath string) error {
	if len(key) > maxEnvKeyBytes {
		return invalidConfig("field %s: derived environment variable exceeds %d bytes", fieldPath, maxEnvKeyBytes)
	}
	return nil
}

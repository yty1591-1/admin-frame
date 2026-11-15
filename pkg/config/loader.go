package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

const (
	// maxErrorDetail 限制错误摘要长度。
	maxErrorDetail = 512
	// maxIncludeDepth 限制 include 嵌套层数，避免异常配置拖垮启动。
	maxIncludeDepth = 8
	// includeKey 是文件顶层声明依赖模块的保留字段。
	includeKey = "include"
)

// backtickLiteral 匹配 YAML 类型错误里以反引号包裹的取值。
//
// yaml.v3 的 TypeError 形如 "cannot unmarshal !!str `secret` into int"，
// 直接透传会把配置值写进启动日志，因此统一替换为占位符。
var backtickLiteral = regexp.MustCompile("`[^`\n]*`")

// Options 是 Loader 的构造参数。
//
// 零值不可用：Files 与 EnvPrefix 至少提供一个来源。
type Options[T any] struct {
	// Files 是 YAML 文件路径，按顺序依次解码到同一结构体；
	// 后面的文件覆盖前面已出现的字段，未出现的字段保持不变。
	//
	// 单个文件可通过顶层 `include:` 声明依赖文件，被包含文件先于当前文件加载，
	// 路径相对声明它的文件解析，同样遵循“后者覆盖前者”。
	Files []string
	// EnvPrefix 是环境变量前缀（如 "ADMIN_FRAME"）；为空表示禁用环境变量覆盖。
	//
	// 前缀只允许字母、数字、下划线与 -/./，内部统一转为大写并把分隔符换成 "_"。
	EnvPrefix string
	// LookupEnv 读取环境变量；nil 时使用 os.LookupEnv。
	LookupEnv func(key string) (value string, ok bool)
	// Values 返回配置中心快照的扁平取值（yaml 路径 → 字符串）。
	//
	// 在文件与环境变量之后覆盖，优先级最高（CFG-04：配置中心 > 代码默认值）；
	// 未出现在快照中的键保持文件/环境变量/默认值，便于迁移期只接管已声明键。
	// nil 表示不接入配置中心；返回错误则整体加载失败。
	Values func() (map[string]string, error)
	// AllowMissingFiles 允许 opts.Files 中的文件不存在（无文件启动演练）。
	//
	// 仅跳过顶层文件缺失；include 缺失仍然报错，避免静默丢模块。开启后
	// 文件层不参与取值，由默认值 / 环境变量 / 配置中心快照提供。
	AllowMissingFiles bool
	// ValueAliases 把配置中心键映射到配置文件 yaml 路径。
	//
	// 用于键名遵循 <domain>.<name>、但历史配置字段在顶层的场景
	// （如 server.addr → addr）；别名只影响快照覆盖，不改变对外键名。
	ValueAliases map[string]string
	// Defaults 在解码文件前写入默认值；可为 nil。
	Defaults func(*T)
	// Normalize 在环境变量覆盖之后、Validate 之前执行；可为 nil。
	//
	// 用于跨字段派生默认值（例如由 channel 推导 expected_channel），
	// 保证环境变量改写前置字段后，派生值仍然一致；返回错误则整体加载失败，
	// 用于把无法延后的依赖解析（如密钥读取）纳入启动错误链。
	Normalize func(*T) error
	// Validate 在环境变量覆盖后执行；返回非 nil 则整体加载失败（CFG-03）。
	//
	// 为 nil 时，若 T 实现了 Validatable 则自动调用其 Validate；
	// 包不做隐式校验之外的兜底；生产配置应始终提供其中一种校验。
	// 错误文本会进入启动日志，不得包含口令、DSN 等敏感取值。
	Validate func(*T) error
	// Decode 覆盖默认的严格 YAML 解码；可用于 JSON 或加密配置。
	//
	// 实现必须把数据解码进 dst 已存在的字段语义（便于多文件覆盖）。
	// 覆盖后不再解析 `include:`，需要分层时请保留默认 YAML 解码。
	Decode func(data []byte, dst *T) error
	// AllowUnknownFields 关闭未知字段拒绝；默认 false（严格模式）。
	AllowUnknownFields bool
}

// Loader 按「默认值 →（可选）文件 → 环境变量 → 配置中心快照 → 校验」加载 T。
//
// M3 目标态（CFG-04）：业务配置以「配置中心快照 > 代码默认值」为准；
// AllowMissingFiles=true 时跳过缺失文件，文件层不参与取值。基础设施口令
// 仍可通过 EnvPrefix / Secret Provider 注入，不属于业务配置层。
//
// 构造后只读，可并发调用 Load；每次调用返回独立副本。
type Loader[T any] struct {
	opts Options[T]
	// expandIncludes 仅在默认 YAML 解码时开启；自定义 Decode 由调用方自行处理分层。
	expandIncludes bool
}

// Validatable 是配置自校验约定；Options.Validate 为 nil 时自动调用。
//
// 与 internal/platform 中 Config.Validate 的既有习惯对齐（CFG-03）。
type Validatable interface {
	// Validate 返回非 nil 表示配置不可用；错误文本不应包含敏感取值。
	Validate() error
}

// NewLoader 校验参数并构造 Loader。
//
// 参数非法时返回包装 ErrInvalidConfig 的错误，应在启动期终止进程。
func NewLoader[T any](opts Options[T]) (loader *Loader[T], err error) {
	if len(opts.Files) == 0 && strings.TrimSpace(opts.EnvPrefix) == "" {
		return nil, invalidConfig("at least one configuration source is required (Files or EnvPrefix)")
	}
	seen := make(map[string]struct{}, len(opts.Files))
	files := make([]string, len(opts.Files))
	for i, path := range opts.Files {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return nil, invalidConfig("file path at index %d must not be empty", i)
		}
		if _, ok := seen[trimmed]; ok {
			return nil, invalidConfig("duplicate configuration file %q", trimmed)
		}
		seen[trimmed] = struct{}{}
		files[i] = trimmed
	}
	opts.Files = files
	prefix, err := normalizePrefix(opts.EnvPrefix)
	if err != nil {
		return nil, err
	}
	opts.EnvPrefix = prefix
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	expand := false
	if opts.Decode == nil {
		allowUnknown := opts.AllowUnknownFields
		opts.Decode = func(data []byte, dst *T) error {
			return decodeYAML(data, dst, allowUnknown)
		}
		expand = true
	}
	return &Loader[T]{opts: opts, expandIncludes: expand}, nil
}

// Load 按优先级加载并校验配置，返回独立副本。
//
// 执行顺序：Defaults → Files（按声明顺序，展开 include）→ 环境变量 →
// 配置中心快照 → Normalize → Validate。
// 失败返回包装 ErrInvalidConfig 的错误，并按原因附加 ErrMissingFile、
// ErrFileUnreadable、ErrMalformedFile、ErrUnknownField、ErrEnvOverride 或
// ErrSnapshotOverride；语义码为 InvalidArgument（文件不存在为 NotFound）。
// 错误文本不含配置值。
func (l *Loader[T]) Load() (cfg *T, err error) {
	if l == nil {
		return nil, invalidConfig("nil loader")
	}
	var value T
	if l.opts.Defaults != nil {
		l.opts.Defaults(&value)
	}
	for _, path := range l.opts.Files {
		if err := l.loadFile(path, &value); err != nil {
			if l.opts.AllowMissingFiles && errors.Is(err, ErrMissingFile) {
				continue
			}
			return nil, err
		}
	}
	if l.opts.EnvPrefix != "" {
		if err := BindEnv(&value, l.opts.EnvPrefix, l.opts.LookupEnv); err != nil {
			return nil, err
		}
	}
	if l.opts.Values != nil {
		values, err := l.opts.Values()
		if err != nil {
			return nil, normalizeError(err)
		}
		if err := BindValues(&value, applyValueAliases(values, l.opts.ValueAliases)); err != nil {
			return nil, err
		}
	}
	if l.opts.Normalize != nil {
		if err := l.opts.Normalize(&value); err != nil {
			return nil, normalizeError(err)
		}
	}
	if err := l.validate(&value); err != nil {
		return nil, err
	}
	return &value, nil
}

// validate 优先使用显式回调，其次使用 Validatable 约定。
func (l *Loader[T]) validate(value *T) error {
	var err error
	if l.opts.Validate != nil {
		err = l.opts.Validate(value)
	} else if validatable, ok := any(value).(Validatable); ok {
		err = validatable.Validate()
	}
	if err == nil {
		return nil
	}
	return validationError(err)
}

// loadFile 读取并解码单个文件；空文件视为无覆盖。
func (l *Loader[T]) loadFile(path string, dst *T) error {
	return l.loadFileChain(path, dst, nil)
}

// loadFileChain 先按声明顺序展开 include，再解码当前文件（当前文件优先级最高）。
//
// chain 保存当前包含链的绝对路径，用于检测循环与限制嵌套深度。
func (l *Loader[T]) loadFileChain(path string, dst *T, chain []string) error {
	if !l.expandIncludes {
		data, err := os.ReadFile(path) // #nosec G304,G703 -- 路径来自运维配置，非请求输入。
		if err != nil {
			return readError(path, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := l.opts.Decode(data, dst); err != nil {
			return decodeErrorOf(path, err)
		}
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return readError(path, err)
	}
	for _, ancestor := range chain {
		if ancestor == abs {
			return decodeError(ErrMalformedFile, path, "include cycle detected")
		}
	}
	if len(chain) >= maxIncludeDepth {
		return decodeError(ErrMalformedFile, path,
			fmt.Sprintf("include depth exceeds %d", maxIncludeDepth))
	}

	data, err := os.ReadFile(path) // #nosec G304,G703 -- 路径来自运维配置，非请求输入。
	if err != nil {
		return readError(path, err)
	}
	includes, body, err := splitInclude(data, path)
	if err != nil {
		return err
	}
	for _, included := range includes {
		includedPath := included
		if !filepath.IsAbs(includedPath) {
			includedPath = filepath.Join(filepath.Dir(path), includedPath)
		}
		if err := l.loadFileChain(includedPath, dst, append(chain, abs)); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := l.opts.Decode(body, dst); err != nil {
		return decodeErrorOf(path, err)
	}
	return nil
}

// splitInclude 提取顶层 include 列表并返回去掉该字段后的文档。
//
// 无 include 时原样返回 data，避免不必要的重编码；有 include 时用 map 承载剩余
// 字段后重新编码，最终严格解码仍会拒绝未知字段。
func splitInclude(data []byte, path string) (paths []string, encoded []byte, marshalErr error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, nil
		}
		return nil, nil, decodeErrorOf(path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, nil, decodeError(ErrMalformedFile, path, "multiple YAML documents are not supported")
	}
	value, ok := raw[includeKey]
	if !ok {
		return nil, data, nil
	}
	delete(raw, includeKey)

	items, ok := value.([]any)
	if !ok {
		return nil, nil, decodeError(ErrMalformedFile, path, "include must be a list of file paths")
	}
	includes := make([]string, 0, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, nil, decodeError(ErrMalformedFile, path,
				fmt.Sprintf("include entry %d must be a non-empty string", i))
		}
		includes = append(includes, strings.TrimSpace(text))
	}
	if len(raw) == 0 {
		return includes, nil, nil
	}
	encoded, marshalErr = yaml.Marshal(raw)
	if marshalErr != nil {
		return nil, nil, decodeError(ErrMalformedFile, path, "cannot re-encode config without include")
	}
	return includes, encoded, nil
}

// files 返回文件列表副本，供 Watcher 创建时固定监视目标。
func (l *Loader[T]) files() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.opts.Files))
	copy(out, l.opts.Files)
	return out
}

// decodeYAML 严格解码单个 YAML 文档，拒绝多文档与未知字段。
func decodeYAML[T any](data []byte, dst *T, allowUnknown bool) error {
	if dst == nil {
		return errors.New("nil decode target")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(!allowUnknown)
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple YAML documents are not supported")
	}
	return nil
}

// readError 将文件读取错误映射为细分哨兵。
func readError(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return configError(ErrMissingFile, pkgerrors.NotFound(fmt.Sprintf("file %q does not exist", path)))
	}
	if errors.Is(err, os.ErrPermission) {
		return configError(ErrFileUnreadable, pkgerrors.PermissionDenied(fmt.Sprintf("file %q is not readable", path)))
	}
	return configError(ErrFileUnreadable,
		pkgerrors.Wrap(err, fmt.Sprintf("file %q", path)).WithCode(pkgerrors.CodeUnavailable))
}

// decodeErrorOf 脱敏后包装 YAML 错误，并标记未知字段。
func decodeErrorOf(path string, err error) error {
	detail := sanitizeError(err)
	if strings.Contains(detail, "not found in type") || strings.Contains(detail, "unknown field") {
		return decodeError(ErrUnknownField, path, detail)
	}
	return decodeError(ErrMalformedFile, path, detail)
}

// sanitizeError 移除反引号包裹的取值并把多行压成一行，限制长度。
func sanitizeError(err error) string {
	detail := backtickLiteral.ReplaceAllString(err.Error(), "`<redacted>`")
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > maxErrorDetail {
		detail = detail[:maxErrorDetail] + "..."
	}
	return detail
}

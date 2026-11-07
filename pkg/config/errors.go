package config

import (
	"fmt"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

var (
	// ErrInvalidConfig 是所有加载/校验失败的共同哨兵，便于调用方统一判定。
	ErrInvalidConfig = pkgerrors.NewStd("config: invalid config")
	// ErrMissingFile 表示配置文件不存在。
	ErrMissingFile = pkgerrors.NewStd("config: file not found")
	// ErrFileUnreadable 表示配置文件存在但无法读取（权限、IO 错误等）。
	ErrFileUnreadable = pkgerrors.NewStd("config: file unreadable")
	// ErrMalformedFile 表示文件内容不是合法 YAML 或含多文档。
	ErrMalformedFile = pkgerrors.NewStd("config: malformed file")
	// ErrUnknownField 表示 YAML 中存在结构体未声明的字段（严格模式）。
	ErrUnknownField = pkgerrors.NewStd("config: unknown field")
	// ErrEnvOverride 表示环境变量无法解析或目标字段不受支持。
	ErrEnvOverride = pkgerrors.NewStd("config: invalid environment override")
	// ErrUnsupportedField 表示显式 env 标签标注在不支持绑定的字段上。
	ErrUnsupportedField = pkgerrors.NewStd("config: unsupported field type")
	// ErrWatcherClosed 表示 Watcher 已关闭，不能再启动或手动重载。
	ErrWatcherClosed = pkgerrors.NewStd("config: watcher closed")
	// ErrWatcherStarted 表示 Watcher 已在运行。
	ErrWatcherStarted = pkgerrors.NewStd("config: watcher already started")
)

// invalidConfig 构造加载器/监视器配置错误。
//
// 消息只包含字段路径、文件路径与环境变量名，不得包含配置值。
func invalidConfig(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidConfig,
		pkgerrors.InvalidArgument(fmt.Sprintf(format, args...)))
}

// configError 组合共同哨兵、细分哨兵与语义错误，保证 errors.Is 与
// errors.IsCode 均可判定。
func configError(kind, semantic error) error {
	return fmt.Errorf("%w: %w: %w", ErrInvalidConfig, kind, semantic)
}

// decodeError 构造解析错误；detail 必须已脱敏（不含配置值）。
func decodeError(kind error, path, detail string) error {
	return configError(kind, pkgerrors.InvalidArgument(fmt.Sprintf("file %q: %s", path, detail)))
}

// validationError 包装校验失败，统一语义码并保留原因链。
func validationError(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidConfig,
		pkgerrors.Wrap(err, "config validation failed").WithCode(pkgerrors.CodeInvalidArgument))
}

// normalizeError 包装 Normalize 阶段的失败，保留原始错误链。
//
// 归一化可能触发依赖解析（如密钥读取），失败属于启动期不可用而非参数错误，
// 因此只挂共同哨兵，不改写底层语义码。
func normalizeError(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
}

// envError 构造环境变量绑定错误；只输出变量名与字段路径，不输出取值。
func envError(key, reason string) error {
	return configError(ErrEnvOverride,
		pkgerrors.InvalidArgument(fmt.Sprintf("environment variable %s: %s", key, reason)))
}

// unsupportedFieldError 构造不支持字段的错误；field 为结构体路径。
func unsupportedFieldError(field, key string) error {
	return configError(ErrUnsupportedField,
		pkgerrors.InvalidArgument(fmt.Sprintf("field %s: type does not support environment binding (variable %s)", field, key)))
}

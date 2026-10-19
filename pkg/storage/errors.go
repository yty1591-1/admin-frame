package storage

import stderrors "errors"

var (
	// ErrNotFound 对象不存在。
	ErrNotFound = stderrors.New("storage: not found")
	// ErrInvalidKey 对象键非法（空、越界、路径穿越等）。
	ErrInvalidKey = stderrors.New("storage: invalid key")
	// ErrInvalidConfig 配置不合法。
	ErrInvalidConfig = stderrors.New("storage: invalid config")
	// ErrClosed 存储已关闭。
	ErrClosed = stderrors.New("storage: closed")
	// ErrTooLarge 对象超过 MaxObjectBytes。
	ErrTooLarge = stderrors.New("storage: object too large")
	// ErrUnsupported 当前后端不支持该能力（如预签名）。
	ErrUnsupported = stderrors.New("storage: unsupported")
	// ErrAlreadyExists 在 IfNoneMatch/独占写入场景下键已存在（预留）。
	ErrAlreadyExists = stderrors.New("storage: already exists")
	// ErrUnknownDriver 未注册或未 blank-import 的驱动名。
	ErrUnknownDriver = stderrors.New("storage: unknown driver")
)

package cache

import stderrors "errors"

var (
	// ErrInvalidConfig 表示缓存配置不合法（缺层、TTL 非正、key 为空）。
	ErrInvalidConfig = stderrors.New("iam cache: invalid config")
	// ErrCorrupt 表示缓存值无法按当前 schema 解码；调用方必须视为 miss。
	ErrCorrupt = stderrors.New("iam cache: corrupt snapshot")
)

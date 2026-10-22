package testkit

import (
	"errors"
	"path/filepath"
	"runtime"
)

// RepoRoot 返回仓库根目录。
//
// 以本文件位置回溯（pkg/testkit → 仓库根），不依赖进程工作目录，避免测试从子目录启动时失效。
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("testkit: cannot resolve source path")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

package main

import (
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

// runtimeVersion 返回构建使用的 Go 版本，供 -version 输出。
func runtimeVersion() string { return runtime.Version() }

// printStatus 输出迁移状态；dirty / drift / missing 均显式标记，便于 CI 与排障。
func printStatus(w io.Writer, st migrate.Status) {
	reportf(w, "current version: %06d\n", st.Current)
	reportf(w, "dirty: %t\n", st.Dirty)
	reportf(w, "applied: %d\n", len(st.Applied))
	for _, rec := range st.Applied {
		marker := ""
		if rec.Dirty {
			marker = " dirty"
		}
		reportf(w, "  %06d_%s applied_at=%s%s\n", rec.Version, rec.Description, rec.AppliedAt.UTC().Format(time.RFC3339), marker)
	}
	if len(st.Pending) == 0 {
		reportf(w, "pending: none\n")
	} else {
		reportf(w, "pending:\n")
		for _, m := range st.Pending {
			reportf(w, "  %06d_%s\n", m.Version, m.Description)
		}
	}
	// drift / missing 与 pending 无关，必须始终输出，否则无待执行项时会掩盖异常。
	if len(st.Drift) > 0 {
		reportf(w, "drift (checksum mismatch): %v\n", st.Drift)
	}
	if len(st.Missing) > 0 {
		reportf(w, "missing from source: %v\n", st.Missing)
	}
}

// reportf 写 CLI 输出；写入失败无可恢复动作（终端断开时进程通常已结束），故忽略。
func reportf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

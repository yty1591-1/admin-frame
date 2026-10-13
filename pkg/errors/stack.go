package errors

import (
	"fmt"
	"io"
	"path"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	defaultStackDepth = 32
	// callerSkip：Callers(0) / callers(1) / newAt(2) / 导出函数(3) / 业务调用方(4)
	callerSkip = 4
)

// captureStackEnabled 控制 New/Wrap 是否打栈；热路径可关闭。
var captureStackEnabled atomic.Bool

func init() {
	captureStackEnabled.Store(true)
}

// SetCaptureStack 全局开关：是否在 New/Wrap 时捕获调用栈。
// 测试或极热路径可关闭；默认开启。
func SetCaptureStack(enabled bool) {
	captureStackEnabled.Store(enabled)
}

// CaptureStackEnabled 返回当前是否捕获调用栈。
func CaptureStackEnabled() bool {
	return captureStackEnabled.Load()
}

// Frame 表示栈上一帧。
type Frame uintptr

// pc 返回程序计数器。
func (f Frame) pc() uintptr { return uintptr(f) - 1 }

// File 返回源文件路径。
func (f Frame) File() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	file, _ := fn.FileLine(f.pc())
	return file
}

// Line 返回行号。
func (f Frame) Line() int {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return 0
	}
	_, line := fn.FileLine(f.pc())
	return line
}

// Name 返回函数名。
func (f Frame) Name() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	return fn.Name()
}

// Format 按 fmt 约定格式化单帧。
//
//	%s    源文件名（basename）
//	%d    行号
//	%n    函数名（短名）
//	%v    等价于 %s:%d
//	%+s   完整路径 + 函数名
//	%+v   完整路径、函数名与行号
func (f Frame) Format(s fmt.State, verb rune) {
	switch verb {
	case 's':
		switch {
		case s.Flag('+'):
			_, _ = io.WriteString(s, f.Name())
			_, _ = io.WriteString(s, "\n\t")
			_, _ = io.WriteString(s, f.File())
		default:
			_, _ = io.WriteString(s, path.Base(f.File()))
		}
	case 'd':
		_, _ = io.WriteString(s, strconv.Itoa(f.Line()))
	case 'n':
		_, _ = io.WriteString(s, shortFuncName(f.Name()))
	case 'v':
		f.Format(s, 's')
		_, _ = io.WriteString(s, ":")
		f.Format(s, 'd')
	}
}

// StackTrace 是有序栈帧（最靠近错误创建点的在前）。
type StackTrace []Frame

// Format 格式化整段栈。
func (st StackTrace) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case s.Flag('+'):
			for _, f := range st {
				_, _ = io.WriteString(s, "\n")
				f.Format(s, verb)
			}
		case s.Flag('#'):
			_, _ = fmt.Fprintf(s, "%#v", []Frame(st))
		default:
			_, _ = fmt.Fprintf(s, "%v", []Frame(st))
		}
	}
}

type stack []uintptr

// Format 实现 fmt.Formatter，输出调用栈。
func (s *stack) Format(st fmt.State, verb rune) {
	StackTrace(frames(*s)).Format(st, verb)
}

func (s *stack) stackTrace() StackTrace {
	return frames(*s)
}

func callers(skip int) *stack {
	if !captureStackEnabled.Load() {
		return nil
	}
	var pcs [defaultStackDepth]uintptr
	n := runtime.Callers(skip, pcs[:])
	if n == 0 {
		return nil
	}
	var st stack = pcs[0:n]
	return &st
}

func frames(pcs []uintptr) StackTrace {
	fs := make(StackTrace, len(pcs))
	for i, pc := range pcs {
		fs[i] = Frame(pc)
	}
	return fs
}

func shortFuncName(name string) string {
	// runtime/debug 风格：取最后一段
	i := len(name) - 1
	for i >= 0 && name[i] != '/' {
		i--
	}
	name = name[i+1:]
	i = len(name) - 1
	for i >= 0 && name[i] != '.' {
		i--
	}
	return name[i+1:]
}

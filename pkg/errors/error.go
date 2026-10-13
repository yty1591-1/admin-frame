package errors

import (
	"fmt"
	"io"
)

// Error 是带语义码、因果链、栈与结构化字段的富错误类型。
//
// 构造阶段可通过 With* 链式补充元数据；构造完成后应视为只读。
type Error struct {
	code      Code
	msg       string
	detail    string
	op        string
	cause     error
	fields    []Field
	stack     *stack
	retryable bool
	retrySet  bool // 区分「未设置」与「显式 false」
}

// New 创建带消息的错误，并在创建点捕获调用栈。
func New(msg string) *Error {
	return newAt(callerSkip, msg, nil)
}

// Newf 按格式创建错误。
func Newf(format string, args ...any) *Error {
	return newAt(callerSkip, fmt.Sprintf(format, args...), nil)
}

// Wrap 包装 cause 并附加消息。cause 为 nil 时返回 nil。
// 若 cause 已是 *Error 且带栈，则不重复打栈。
func Wrap(cause error, msg string) *Error {
	if cause == nil {
		return nil
	}
	return newAt(callerSkip, msg, cause)
}

// Wrapf 格式化消息后包装 cause。
func Wrapf(cause error, format string, args ...any) *Error {
	if cause == nil {
		return nil
	}
	return newAt(callerSkip, fmt.Sprintf(format, args...), cause)
}

func newAt(skip int, msg string, cause error) *Error {
	e := &Error{
		msg:   msg,
		cause: cause,
	}
	if cause != nil {
		if ce, ok := cause.(*Error); ok {
			if ce.stack != nil {
				e.stack = ce.stack
			}
			if ce.code != "" {
				e.code = ce.code
			}
			if ce.retrySet {
				e.retryable = ce.retryable
				e.retrySet = true
			}
			if len(ce.fields) > 0 && len(e.fields) == 0 {
				e.fields = append([]Field(nil), ce.fields...)
			}
		}
	}
	if e.stack == nil {
		e.stack = callers(skip)
	}
	return e
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.op != "" && e.msg != "" && e.cause != nil:
		return fmt.Sprintf("%s: %s: %v", e.op, e.msg, e.cause)
	case e.op != "" && e.msg != "":
		return fmt.Sprintf("%s: %s", e.op, e.msg)
	case e.op != "" && e.cause != nil:
		return fmt.Sprintf("%s: %v", e.op, e.cause)
	case e.msg != "" && e.cause != nil:
		return fmt.Sprintf("%s: %v", e.msg, e.cause)
	case e.msg != "":
		return e.msg
	case e.cause != nil:
		return e.cause.Error()
	case e.code != "":
		return e.code.String()
	default:
		return "error"
	}
}

// Unwrap 返回被包装的 cause，供 errors.Is / As 遍历。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is 使 errors.Is 能按哨兵或同 Code 匹配。
func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return e == target
	}
	if t, ok := target.(*Error); ok {
		if t.code != "" && e.code != "" && e.code == t.code && t.msg == "" && t.cause == nil {
			// 仅带 Code 的哨兵（如 ErrNotFound）
			return true
		}
		if t.msg != "" && e.msg == t.msg && e.code == t.code && t.cause == nil && len(t.fields) == 0 {
			return true
		}
	}
	return false
}

// Code 返回语义错误码；未设置时为 CodeUnknown。
func (e *Error) Code() Code {
	if e == nil || e.code == "" {
		return CodeUnknown
	}
	return e.code
}

// Message 返回面向调用方的消息（不含 cause 拼接）。
func (e *Error) Message() string {
	if e == nil {
		return ""
	}
	return e.msg
}

// Detail 返回仅供内部排障的详情，不得直接返回给终端用户。
func (e *Error) Detail() string {
	if e == nil {
		return ""
	}
	return e.detail
}

// Op 返回出错操作名（如 "order.Create"）。
func (e *Error) Op() string {
	if e == nil {
		return ""
	}
	return e.op
}

// Fields 返回结构化字段副本。
func (e *Error) Fields() []Field {
	if e == nil || len(e.fields) == 0 {
		return nil
	}
	out := make([]Field, len(e.fields))
	copy(out, e.fields)
	return out
}

// Retryable 报告是否建议调用方重试。
// 未显式设置时，按 Code 给出默认策略。
func (e *Error) Retryable() bool {
	if e == nil {
		return false
	}
	if e.retrySet {
		return e.retryable
	}
	switch e.Code() {
	case CodeUnavailable, CodeDeadlineExceeded, CodeResourceExhausted, CodeAborted:
		return true
	default:
		return false
	}
}

// StackTrace 返回创建点调用栈；未捕获时为 nil。
func (e *Error) StackTrace() StackTrace {
	if e == nil || e.stack == nil {
		return nil
	}
	return e.stack.stackTrace()
}

// PublicMessage 返回可安全暴露给客户端的消息。
// 对非 ClientSafe 的码，返回通用文案，避免泄露内部细节。
func (e *Error) PublicMessage() string {
	if e == nil {
		return "error"
	}
	if e.Code().ClientSafe() && e.msg != "" {
		return e.msg
	}
	switch e.Code() {
	case CodeInternal, CodeUnknown, CodeDataLoss:
		return "internal error"
	default:
		if e.msg != "" {
			return e.msg
		}
		return e.Code().String()
	}
}

// WithCode 设置语义码，返回自身以支持链式调用。
func (e *Error) WithCode(code Code) *Error {
	if e == nil {
		return nil
	}
	e.code = code
	return e
}

// WithMessage 覆盖对外消息。
func (e *Error) WithMessage(msg string) *Error {
	if e == nil {
		return nil
	}
	e.msg = msg
	return e
}

// WithDetail 设置内部详情。
func (e *Error) WithDetail(detail string) *Error {
	if e == nil {
		return nil
	}
	e.detail = detail
	return e
}

// WithDetailf 格式化内部详情。
func (e *Error) WithDetailf(format string, args ...any) *Error {
	if e == nil {
		return nil
	}
	e.detail = fmt.Sprintf(format, args...)
	return e
}

// WithOp 设置操作名。
func (e *Error) WithOp(op string) *Error {
	if e == nil {
		return nil
	}
	e.op = op
	return e
}

// WithField 追加一个结构化字段。
func (e *Error) WithField(key string, value any) *Error {
	if e == nil {
		return nil
	}
	e.fields = append(e.fields, F(key, value))
	return e
}

// WithFields 追加多个结构化字段。
func (e *Error) WithFields(fields ...Field) *Error {
	if e == nil {
		return nil
	}
	e.fields = append(e.fields, fields...)
	return e
}

// WithRetryable 显式标记是否可重试。
func (e *Error) WithRetryable(retryable bool) *Error {
	if e == nil {
		return nil
	}
	e.retryable = retryable
	e.retrySet = true
	return e
}

// WithStack 强制在当前调用点重新捕获栈（覆盖已有栈）。
func (e *Error) WithStack() *Error {
	if e == nil {
		return nil
	}
	// WithStack -> callers -> Callers，业务调用方为 skip=3
	e.stack = callers(3)
	return e
}

// Format 支持 fmt 打印：
//
//	%v / %s  — Error() 文本
//	%+v      — 含 code、op、detail、fields、cause 与栈
func (e *Error) Format(s fmt.State, verb rune) {
	if e == nil {
		_, _ = io.WriteString(s, "<nil>")
		return
	}
	switch verb {
	case 'v':
		if s.Flag('+') {
			e.formatVerbose(s)
			return
		}
		fallthrough
	case 's':
		_, _ = io.WriteString(s, e.Error())
	case 'q':
		_, _ = fmt.Fprintf(s, "%q", e.Error())
	}
}

func (e *Error) formatVerbose(s fmt.State) {
	_, _ = io.WriteString(s, e.Error())
	if e.code != "" {
		_, _ = fmt.Fprintf(s, "\ncode: %s", e.code)
	}
	if e.op != "" {
		_, _ = fmt.Fprintf(s, "\nop: %s", e.op)
	}
	if e.detail != "" {
		_, _ = fmt.Fprintf(s, "\ndetail: %s", e.detail)
	}
	if e.retrySet {
		_, _ = fmt.Fprintf(s, "\nretryable: %v", e.retryable)
	}
	for _, f := range e.fields {
		_, _ = fmt.Fprintf(s, "\nfield.%s: %v", f.Key, f.Value)
	}
	if e.stack != nil {
		_, _ = io.WriteString(s, "\nstack:")
		e.stack.Format(s, 'v')
	}
	if e.cause != nil {
		_, _ = fmt.Fprintf(s, "\ncaused by: %+v", e.cause)
	}
}

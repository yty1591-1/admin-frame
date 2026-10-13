package errors

var (
	// ErrUnknown 未分类哨兵。
	ErrUnknown = &Error{code: CodeUnknown}
	// ErrInvalidArgument 参数非法哨兵。
	ErrInvalidArgument = &Error{code: CodeInvalidArgument}
	// ErrFailedPrecondition 前置条件失败哨兵。
	ErrFailedPrecondition = &Error{code: CodeFailedPrecondition}
	// ErrNotFound 资源不存在哨兵，可用 errors.Is 匹配。
	ErrNotFound = &Error{code: CodeNotFound}
	// ErrAlreadyExists 已存在/冲突哨兵。
	ErrAlreadyExists = &Error{code: CodeAlreadyExists}
	// ErrPermissionDenied 无权限哨兵。
	ErrPermissionDenied = &Error{code: CodePermissionDenied}
	// ErrUnauthenticated 未认证哨兵。
	ErrUnauthenticated = &Error{code: CodeUnauthenticated}
	// ErrResourceExhausted 资源耗尽哨兵。
	ErrResourceExhausted = &Error{code: CodeResourceExhausted}
	// ErrCanceled 取消哨兵。
	ErrCanceled = &Error{code: CodeCanceled}
	// ErrDeadlineExceeded 超时哨兵。
	ErrDeadlineExceeded = &Error{code: CodeDeadlineExceeded}
	// ErrAborted 中止哨兵。
	ErrAborted = &Error{code: CodeAborted}
	// ErrOutOfRange 越界哨兵。
	ErrOutOfRange = &Error{code: CodeOutOfRange}
	// ErrUnimplemented 未实现哨兵。
	ErrUnimplemented = &Error{code: CodeUnimplemented}
	// ErrUnavailable 不可用哨兵。
	ErrUnavailable = &Error{code: CodeUnavailable}
	// ErrDataLoss 数据丢失哨兵。
	ErrDataLoss = &Error{code: CodeDataLoss}
	// ErrInternal 内部错误哨兵。
	ErrInternal = &Error{code: CodeInternal}
)

// InvalidArgument 构造 CodeInvalidArgument 错误并打栈。
func InvalidArgument(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeInvalidArgument)
}

// FailedPrecondition 构造 CodeFailedPrecondition 错误并打栈。
func FailedPrecondition(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeFailedPrecondition)
}

// NotFound 构造 CodeNotFound 错误并打栈。
func NotFound(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeNotFound)
}

// AlreadyExists 构造 CodeAlreadyExists 错误并打栈。
func AlreadyExists(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeAlreadyExists)
}

// PermissionDenied 构造 CodePermissionDenied 错误并打栈。
func PermissionDenied(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodePermissionDenied)
}

// Unauthenticated 构造 CodeUnauthenticated 错误并打栈。
func Unauthenticated(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeUnauthenticated)
}

// ResourceExhausted 构造 CodeResourceExhausted 错误并打栈。
func ResourceExhausted(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeResourceExhausted)
}

// Canceled 构造 CodeCanceled 错误并打栈。
func Canceled(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeCanceled)
}

// DeadlineExceeded 构造 CodeDeadlineExceeded 错误并打栈。
func DeadlineExceeded(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeDeadlineExceeded)
}

// Aborted 构造 CodeAborted 错误并打栈。
func Aborted(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeAborted)
}

// Unimplemented 构造 CodeUnimplemented 错误并打栈。
func Unimplemented(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeUnimplemented)
}

// Unavailable 构造 CodeUnavailable 错误并打栈。
func Unavailable(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeUnavailable)
}

// Internal 构造 CodeInternal 错误并打栈。
func Internal(msg string) *Error {
	return newAt(callerSkip, msg, nil).WithCode(CodeInternal)
}

// InternalWrap 将底层错误包装为内部错误（保留 cause）。
func InternalWrap(cause error, msg string) *Error {
	if cause == nil {
		return newAt(callerSkip, msg, nil).WithCode(CodeInternal)
	}
	return newAt(callerSkip, msg, cause).WithCode(CodeInternal)
}

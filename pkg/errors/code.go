package errors

// Code 是稳定、机器可读的错误语义分类（可移植，不绑定具体业务数字码）。
// 取值对齐常见 gRPC / Google AIP 风格，便于跨协议映射。
type Code string

const (
	// CodeUnknown 表示未分类错误。
	CodeUnknown Code = "unknown"
	// CodeInvalidArgument 表示客户端参数非法。
	CodeInvalidArgument Code = "invalid_argument"
	// CodeFailedPrecondition 表示前置条件不满足。
	CodeFailedPrecondition Code = "failed_precondition"
	// CodeNotFound 表示资源不存在。
	CodeNotFound Code = "not_found"
	// CodeAlreadyExists 表示资源已存在或状态冲突。
	CodeAlreadyExists Code = "already_exists"
	// CodePermissionDenied 表示已认证但无权限。
	CodePermissionDenied Code = "permission_denied"
	// CodeUnauthenticated 表示未认证。
	CodeUnauthenticated Code = "unauthenticated"
	// CodeResourceExhausted 表示配额/限流耗尽。
	CodeResourceExhausted Code = "resource_exhausted"
	// CodeCanceled 表示调用被取消。
	CodeCanceled Code = "canceled"
	// CodeDeadlineExceeded 表示超时。
	CodeDeadlineExceeded Code = "deadline_exceeded"
	// CodeAborted 表示并发冲突导致中止。
	CodeAborted Code = "aborted"
	// CodeOutOfRange 表示超出有效范围。
	CodeOutOfRange Code = "out_of_range"
	// CodeUnimplemented 表示未实现。
	CodeUnimplemented Code = "unimplemented"
	// CodeUnavailable 表示服务暂不可用。
	CodeUnavailable Code = "unavailable"
	// CodeDataLoss 表示不可恢复的数据丢失。
	CodeDataLoss Code = "data_loss"
	// CodeInternal 表示内部错误。
	CodeInternal Code = "internal"
)

// String 返回码的字符串形式。
func (c Code) String() string {
	if c == "" {
		return string(CodeUnknown)
	}
	return string(c)
}

// IsValid 报告是否为已知预定义码（空视为 unknown，合法）。
func (c Code) IsValid() bool {
	switch c {
	case "", CodeUnknown, CodeInvalidArgument, CodeFailedPrecondition,
		CodeNotFound, CodeAlreadyExists, CodePermissionDenied, CodeUnauthenticated,
		CodeResourceExhausted, CodeCanceled, CodeDeadlineExceeded, CodeAborted,
		CodeOutOfRange, CodeUnimplemented, CodeUnavailable, CodeDataLoss, CodeInternal:
		return true
	default:
		return false
	}
}

// ClientSafe 报告该码是否通常可安全向终端用户暴露具体 message。
// Internal / Unknown / DataLoss 等默认不暴露内部细节。
func (c Code) ClientSafe() bool {
	switch c.normalize() {
	case CodeInvalidArgument, CodeFailedPrecondition, CodeNotFound,
		CodeAlreadyExists, CodePermissionDenied, CodeUnauthenticated,
		CodeResourceExhausted, CodeCanceled, CodeDeadlineExceeded,
		CodeAborted, CodeOutOfRange, CodeUnimplemented, CodeUnavailable:
		return true
	default:
		return false
	}
}

func (c Code) normalize() Code {
	if c == "" {
		return CodeUnknown
	}
	return c
}

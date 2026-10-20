package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

var (
	// ErrInvalidConfig 表示配置缺失或非法；应在启动期返回并终止进程。
	ErrInvalidConfig = pkgerrors.NewStd("webhook: invalid config")
	// ErrMissingHeader 表示缺少 webhook-id / timestamp / signature 之一。
	ErrMissingHeader = pkgerrors.NewStd("webhook: missing header")
	// ErrMalformedHeader 表示头格式非法（长度、字符集、Base64 或时间戳规范）。
	ErrMalformedHeader = pkgerrors.NewStd("webhook: malformed header")
	// ErrInvalidSignature 表示签名校验未通过；不区分密钥错误与内容被篡改。
	ErrInvalidSignature = pkgerrors.NewStd("webhook: signature verification failed")
	// ErrUnsupportedVersion 表示签名头中不存在配置的版本。
	ErrUnsupportedVersion = pkgerrors.NewStd("webhook: unsupported signature version")
	// ErrTimestampExpired 表示时间戳早于允许窗口（重放或严重时钟漂移）。
	ErrTimestampExpired = pkgerrors.NewStd("webhook: timestamp expired")
	// ErrTimestampFuture 表示时间戳晚于允许窗口（对端时钟超前或伪造）。
	ErrTimestampFuture = pkgerrors.NewStd("webhook: timestamp in the future")
	// ErrReplay 表示该消息 ID 已被消费；调用方应按重复投递处理。
	ErrReplay = pkgerrors.NewStd("webhook: replay detected")
	// ErrBodyTooLarge 表示请求体超过 MaxBodyBytes。
	ErrBodyTooLarge = pkgerrors.NewStd("webhook: request body too large")
	// ErrBodyUnreadable 表示读取请求体失败（连接中断或下游 Reader 错误）。
	ErrBodyUnreadable = pkgerrors.NewStd("webhook: request body unreadable")
	// ErrNonceUnavailable 表示重放存储不可用；必须 fail closed，不得放行。
	ErrNonceUnavailable = pkgerrors.NewStd("webhook: nonce store unavailable")
	// ErrNonceStoreFull 表示 MemoryNonceStore 达到容量上限；调用方应按不可用处理。
	ErrNonceStoreFull = pkgerrors.NewStd("webhook: nonce store full")
)

// invalidConfig 构造配置错误，语义码固定为 InvalidArgument。
//
// 消息不得包含密钥、签名或完整请求内容。
func invalidConfig(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidConfig, pkgerrors.InvalidArgument(fmt.Sprintf(format, args...)))
}

// headerError 构造头解析错误，附带头名与原因；头名来自配置而非用户数据。
func headerError(sentinel error, name, reason string) error {
	return fmt.Errorf("%w: %w", sentinel, pkgerrors.InvalidArgument(fmt.Sprintf("header %q %s", name, reason)))
}

// signatureMismatch 构造签名不匹配错误，语义码为 Unauthenticated。
func signatureMismatch() error {
	return fmt.Errorf("%w: %w", ErrInvalidSignature, pkgerrors.Unauthenticated("webhook signature mismatch"))
}

// unsupportedVersion 构造版本不支持错误；版本号来自配置，可安全输出。
func unsupportedVersion(version string) error {
	return fmt.Errorf("%w: %w", ErrUnsupportedVersion, pkgerrors.InvalidArgument("no signature with version "+version))
}

// timestampOutOfWindow 构造时间窗错误；不输出具体时间戳，避免日志注入。
func timestampOutOfWindow(sentinel error, tolerance time.Duration) error {
	return fmt.Errorf("%w: %w", sentinel, pkgerrors.Unauthenticated(fmt.Sprintf("timestamp outside +/-%s window", tolerance)))
}

// replayDetected 构造重放错误；消息中不包含消息 ID。
func replayDetected() error {
	return fmt.Errorf("%w: %w", ErrReplay, pkgerrors.AlreadyExists("webhook message already consumed"))
}

// bodyTooLarge 构造体积超限错误；只输出配置上限。
func bodyTooLarge(limit int64) error {
	return fmt.Errorf("%w: %w", ErrBodyTooLarge, pkgerrors.InvalidArgument(fmt.Sprintf("request body exceeds %d bytes", limit)))
}

// bodyUnreadable 构造读取失败错误。
func bodyUnreadable(cause error) error {
	return fmt.Errorf("%w: %w", ErrBodyUnreadable, pkgerrors.Wrap(cause, "read request body").WithCode(pkgerrors.CodeInvalidArgument))
}

// nonceUnavailable 构造重放存储失败错误，语义码为 Unavailable，调用方必须拒绝。
func nonceUnavailable(cause error) error {
	return fmt.Errorf("%w: %w", ErrNonceUnavailable, pkgerrors.Wrap(cause, "claim webhook nonce").WithCode(pkgerrors.CodeUnavailable))
}

// nonceContextError 将 ctx 取消/超时映射为可识别的语义码；仍包装 ErrNonceUnavailable，
// 保证调用方在任何存储失败下都按 fail closed 处理。
func nonceContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("%w: %w", ErrNonceUnavailable, pkgerrors.Canceled("webhook: context canceled during nonce claim"))
	}
	return fmt.Errorf("%w: %w", ErrNonceUnavailable, pkgerrors.DeadlineExceeded("webhook: context deadline exceeded during nonce claim"))
}

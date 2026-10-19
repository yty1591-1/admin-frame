package contextx

import (
	"context"

	"github.com/admin-frame/admin-frame/pkg/logger"
)

// LogArgs 返回可供 slog 使用的键值对（跳过空字段），键名与 OBS 字典一致。
//
//	log.InfoContext(ctx, "msg", contextx.LogArgs(ctx)...)
func LogArgs(ctx context.Context) []any {
	id := IdentityFrom(ctx)
	args := make([]any, 0, 10)
	if id.RequestID != "" {
		args = append(args, logger.FieldTraceID, id.RequestID)
	}
	if id.UserID != "" {
		args = append(args, logger.FieldUserID, id.UserID)
	}
	if id.MerchantID != "" {
		args = append(args, logger.FieldMerchant, id.MerchantID)
	}
	if id.Channel != "" {
		args = append(args, logger.FieldChannel, id.Channel)
	}
	return args
}

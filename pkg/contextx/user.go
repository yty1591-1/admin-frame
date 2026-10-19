package contextx

import "context"

// WithUserID 写入主体用户 / 员工 ID（subject_id / user_id）。
func WithUserID(ctx context.Context, userID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, userKey{}, userID)
}

// UserID 读取主体 ID；缺失返回空串。
func UserID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(userKey{}).(string); ok {
		return v
	}
	return ""
}

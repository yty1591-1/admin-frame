package contextx

import "context"

// Identity 是与业务 Principal 对齐的轻量快照（无权限策略）。
type Identity struct {
	RequestID  string
	UserID     string // subject / user_id
	MerchantID string // tenant；B 端必填场景由调用方保证
	Channel    string // admin / biz / c / worker
	Roles      []string
}

// WithIdentity 写入非空身份字段；空字段不覆盖已有值。
func WithIdentity(ctx context.Context, id Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id.RequestID != "" {
		ctx = WithRequestID(ctx, id.RequestID)
	}
	if id.UserID != "" {
		ctx = WithUserID(ctx, id.UserID)
	}
	if id.MerchantID != "" {
		ctx = WithMerchantID(ctx, id.MerchantID)
	}
	if id.Channel != "" {
		ctx = WithChannel(ctx, id.Channel)
	}
	if len(id.Roles) > 0 {
		ctx = WithRoles(ctx, id.Roles...)
	}
	return ctx
}

// IdentityFrom 读取快照（缺失字段为空）。
func IdentityFrom(ctx context.Context) Identity {
	return Identity{
		RequestID:  RequestID(ctx),
		UserID:     UserID(ctx),
		MerchantID: MerchantID(ctx),
		Channel:    Channel(ctx),
		Roles:      Roles(ctx),
	}
}

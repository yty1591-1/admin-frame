package auth

import "context"

// Channel 业务端别，与架构文档一致。
type Channel string

const (
	// ChannelAdmin 运营后台（A 端）。
	ChannelAdmin Channel = "admin"
	// ChannelBiz 商家端（B 端）。
	ChannelBiz Channel = "biz"
	// ChannelC 消费者端（C 端）。
	ChannelC Channel = "c"
)

// Principal 认证后的主体上下文。
//
// 请求链路中的 request_id / user_id / merchant_id / channel 原语见 pkg/contextx；
// 鉴权成功后应由 middleware 同时写入 contextx，供非 auth 层只读使用。
type Principal struct {
	Channel    Channel
	SubjectID  string
	MerchantID string
	Roles      []string
	RequestID  string
}

type ctxKey struct{}

// WithPrincipal 将 Principal 写入 context。
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext 读取 Principal；不存在时 ok 为 false。
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// MustFromContext 读取 Principal；缺失时 panic（仅用于已保证鉴权通过的路径）。
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: principal missing from context")
	}
	return p
}

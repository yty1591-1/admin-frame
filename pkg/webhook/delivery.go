package webhook

import (
	"context"
	"time"
)

// Delivery 是通过校验的入站 Webhook 快照。
//
// Body 与输入共享底层数组，调用方不得修改；需要跨请求保留时使用 Clone。
// Timestamp 为 UTC，精度为秒。
type Delivery struct {
	// ID 是发送方的消息唯一 ID，已用于重放去重。
	ID string
	// Timestamp 是签名头中的发送时间。
	Timestamp time.Time
	// Body 是原始请求体，未做任何解码或重编码。
	Body []byte
}

// Clone 返回深拷贝，避免请求结束后 Body 被复用或篡改。
func (d Delivery) Clone() Delivery {
	out := d
	out.Body = append([]byte(nil), d.Body...)
	return out
}

type deliveryContextKey struct{}

// WithDelivery 将已校验的投递写入 context，供下游 Handler 读取。
//
// 典型由 Verifier.Middleware 调用；ctx 必须非 nil。
func WithDelivery(ctx context.Context, delivery Delivery) context.Context {
	return context.WithValue(ctx, deliveryContextKey{}, delivery)
}

// DeliveryFrom 从 context 读取已校验的投递；中间件未执行时返回 false。
func DeliveryFrom(ctx context.Context) (Delivery, bool) {
	delivery, ok := ctx.Value(deliveryContextKey{}).(Delivery)
	return delivery, ok
}

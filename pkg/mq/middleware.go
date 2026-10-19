package mq

// Middleware 装饰 Handler（日志、metrics、tracing 等由调用方注入）。
type Middleware func(next Handler) Handler

// Chain 按顺序组合中间件：先注册的最外层。
func Chain(h Handler, mws ...Middleware) Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			h = mws[i](h)
		}
	}
	return h
}

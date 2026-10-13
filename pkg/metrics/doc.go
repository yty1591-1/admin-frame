// Package metrics 提供基于 Prometheus 的指标注册与常用 HTTP RED 指标。
//
// 设计边界：
//   - 不得依赖 internal/；service/channel/env 由调用方以常量标签注入
//   - 默认使用私有 Registry，避免多进程测试污染全局 DefaultRegisterer
//   - HTTP 路由标签优先使用 ServeMux Pattern，禁止默认用原始 URL.Path（防高基数）
//   - /metrics 暴露与网络隔离由 platform / 运维负责，本包只提供 Handler
//
// 典型用法：
//
//	m, err := metrics.New(metrics.Options{
//		Service: "admin-api", Channel: "admin", Env: "prod",
//	})
//	mux.Handle("GET /metrics", m.Handler())
//	handler = m.HTTP().Middleware()(handler)
package metrics

// Package httpserver 是进程级 HTTP 入口装配：Server 生命周期、中间件管线、
// 租户 / 配额守卫的插入点与探针端点。
//
// 边界：本包只决定「请求怎么串」与「进程怎么起停」，不含业务路由与鉴权实现；
// 路由由 cmd 注册，鉴权中间件由 internal/platform/authn 提供。
package httpserver

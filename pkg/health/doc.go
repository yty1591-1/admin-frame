// Package health 提供 HTTP 存活（liveness）与就绪（readiness）探针。
//
// Readiness 可注册命名 Checker；任一失败时返回 503。
package health

// Package auth 定义跨端共享的认证主体 Principal、Channel 常量与权限中间件契约。
//
// 不含 Token 校验实现；校验在 internal/security 完成，由 cmd 在装配期注入。
package auth

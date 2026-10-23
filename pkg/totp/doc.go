// Package totp 提供基于标准库的 TOTP（RFC 6238）实现。
//
// 约束（docs/identity-authn.md §7.1）：
//   - 仅依赖标准库，不引入第三方；
//   - HMAC-SHA1、30 秒步长、6 位动态码；
//   - 校验允许 ±1 个时间窗口，使用常量时间比较。
package totp

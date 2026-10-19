// Package password 提供密码哈希与校验：bcrypt、argon2id（OWASP 推荐参数）。
//
// 设计边界（对齐 SEC / 架构身份章节）：
//   - 不得依赖 internal/；不存明文、不做可逆「加密密码」
//   - 默认新哈希使用 argon2id；Verify 按编码前缀自动识别算法
//   - 校验失败返回 ErrMismatch，不得区分「用户不存在 / 密码错误」的细节给调用链外
//   - 错误与返回值不得包含明文密码；调用方日志禁止记录密码（SEC-05）
//   - 策略（最短长度、复杂度、历史轮换）属业务层，本包只做哈希原语与长度上限防 DoS
//
// 编码格式：
//   - bcrypt：标准 `$2a$...` / `$2b$...`
//   - argon2id：PHC `$argon2id$v=19$m=...,t=...,p=...$salt$hash`
//
// 典型用法：
//
//	h := password.New(password.Config{}) // 默认 argon2id
//	encoded, err := h.Hash([]byte("secret"))
//	if err := h.Verify(encoded, []byte("secret")); err != nil { ... }
//	if h.NeedsRehash(encoded) { /* 登录成功后用新参数重哈希并落库 */ }
package password

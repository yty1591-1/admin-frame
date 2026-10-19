// Package jwt 提供 JWT 签发与校验原语（无业务鉴权策略）。
//
// 设计边界（对齐 SEC / 架构三端隔离）：
//   - 不得依赖 internal/；channel、RBAC、商户隔离由 platform / app 负责
//   - 校验须指定允许的算法，拒绝 alg=none 与算法混淆
//   - 建议每端独立 Issuer / Audience / 密钥；本包只做密码学与标准声明校验
//   - 禁止在日志中打印完整 Token 或密钥（SEC-05）
//
// 典型用法：
//
//	m, err := jwt.NewHMAC(jwt.HMACConfig{
//		Issuer: "admin-api", Audience: []string{"admin"}, Secret: secret, TTL: time.Hour,
//	})
//	token, err := m.Sign(jwt.Claims{Subject: "u1"})
//	got, err := m.Parse(token)
package jwt

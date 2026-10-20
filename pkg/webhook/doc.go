// Package webhook 提供入站 Webhook 的签名校验、时间窗校验与重放防护，
// 以及出站请求的签名生成。
//
// 线格式（与 Standard Webhooks 兼容，可按需改头名与版本）：
//
//	webhook-id:        消息唯一 ID（发送方生成，同时作为重放键）
//	webhook-timestamp: Unix 秒级时间戳
//	webhook-signature: <version>,<base64(HMAC)>；多个签名以空格分隔
//
// 签名原文为 id + "." + timestamp + "." + 原始请求体，body 不做任何重编码。
// 校验顺序固定为：解析头 → 体积上限 → 校验签名 → 校验时间窗 → 原子占用 nonce。
// 任何一步失败都返回可 errors.Is 匹配的哨兵；错误文本不包含 body、签名或密钥。
//
// 配置中的 Keys 为原始字节。若密钥以 Base64 下发（如 "whsec_..."），
// 先经 DecodeSecret 解码，避免把编码串直接当作密钥导致验签恒失败。
//
// 设计边界：
//   - 不依赖 internal/，不读取配置、环境变量或全局状态（CFG-07）
//   - 不解析 body、不落库、不做业务去重；重放存储由 NonceStore 注入
//   - 日志/Trace/错误不得包含签名、密钥与请求体（SEC-05）；Observer 事件只含低基数标签
//
// 并发：Verifier / Signer 构造后只读，可被多个 goroutine 并发使用；
// MemoryNonceStore 内部按分片加锁，可并发调用。零值不可用，必须经
// NewVerifier / NewSigner 构造。
//
// 多实例：MemoryNonceStore 只在单进程内生效。多副本部署必须注入共享实现
// （如 Redis SET NX EX、数据库唯一键），否则同一条投递可在不同实例重复执行。
//
// 非目标：厂商特有格式（GitHub 的 sha256=、Stripe 的 t=,v1=、微信/支付宝
// RSA 等）。需要时可在调用方做薄适配，或通过 Algorithm 接口实现自定义算法。
//
// 典型用法（入站）：
//
//	secret, err := webhook.DecodeSecret(os.Getenv("WEBHOOK_SECRET"))
//	if err != nil {
//		return err
//	}
//	verifier, err := webhook.NewVerifier(webhook.Config{
//		Keys:  [][]byte{secret},
//		Nonce: webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{}),
//	})
//	if err != nil {
//		return err
//	}
//	mux.Handle("/webhooks/pay", verifier.Middleware()(payHandler))
//
// 典型用法（出站）：
//
//	signer, err := webhook.NewSigner(webhook.Config{Keys: [][]byte{secret}})
//	if err != nil {
//		return err
//	}
//	if err := signer.SignRequest(req, deliveryID, body); err != nil {
//		return err
//	}
package webhook

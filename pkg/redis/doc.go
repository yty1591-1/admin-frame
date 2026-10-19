// Package redis 提供 Redis 客户端装配、分布式锁与 KV 缓存封装。
//
// 设计边界（对齐架构 §十五 / SEC / REL）：
//   - 不得依赖 internal/；进程装配与配置加载留在 platform
//   - 缓存不是数据源真相；须设 TTL；Key 须含租户/用户等隔离维度（见 Key）
//   - 禁止缓存未脱敏敏感信息；日志不得打印 Password（SEC-05）
//   - 分布式锁为单实例 Redis 语义（SET NX + token 解锁），非多节点 Redlock
//   - 本地/多级缓存抽象见 pkg/cache；本包 Cache.Store() 可接入 Multi
//
// 典型用法：
//
//	c, err := redis.Open(ctx, redis.Config{Addr: "127.0.0.1:6379", KeyPrefix: "prod:admin"})
//	defer c.Close()
//	health.Register("redis", c.Checker())
//	lock, err := c.Lock().Acquire(ctx, "order:pay:42", 10*time.Second)
//	defer lock.Unlock(ctx)
package redis

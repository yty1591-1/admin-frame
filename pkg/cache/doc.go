// Package cache 提供本地/多级缓存抽象：统一 Store、内存 LRU+TTL、多层组合与 Cache-Aside。
//
// 设计边界（对齐架构 §十五 / SEC / REL）：
//   - 不得依赖 internal/；Redis 具体客户端见 pkg/redis，经 Store 适配接入
//   - 缓存不是数据源真相；Set / GetOrLoad 必须带 TTL
//   - Key 须含租户/用户等隔离维度；禁止缓存未脱敏敏感信息
//   - 提供单飞合并（击穿）、可选空值短 TTL（穿透）、TTL 抖动（雪崩）
//   - 远端层失败时 Multi 可配置降级（仅用本地），不得拖垮主链路（REL）
//
// 典型用法：
//
//	mem := cache.NewMemory(cache.MemoryConfig{MaxEntries: 10_000})
//	c := cache.New(mem, cache.WithTTL(time.Minute), cache.WithJitter(5*time.Second))
//	val, err := c.GetOrLoad(ctx, key, func(ctx context.Context) ([]byte, error) { return db.Load(ctx) })
package cache

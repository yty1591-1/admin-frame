package app

// Observer 是租户域的可观测端口（T-16）。
//
// 与 audit.Emitter 的区别：Emitter 记录业务事实（谁改了什么），Observer 记录
// 运行信号（守卫判定分布、缓存命中、审计发送失败率）——后者只进指标，
// 不进审计流水。实现方在 cmd 装配期注入（observability 包的 Prometheus 适配）；
// 未注入时各构造函数替换为 noop，业务代码可以无保护调用。
type Observer interface {
	// GuardDecision 记录一次守卫判定结果。
	//
	// result 取值：
	//   - "allow"：放行（含平台级请求空 merchantID 的直接放行——该路径不经过守卫，不计入）
	//   - "deny"：明确拒绝（不在服务期 / 未登记）
	//   - "error"：无法判定（仓储故障，fail closed 映射 503）
	GuardDecision(result string)

	// GuardCacheHit 记录一次守卫缓存查询：true 命中缓存，false 回源数据库。
	//
	// 命中率显著下降通常意味着缓存被大量新商户请求冲刷，或 TTL 配置被调小。
	GuardCacheHit(hit bool)

	// Transition 记录一次成功落库的状态迁移 from → to。
	//
	// 只记成功：失败（非法迁移 / CAS 冲突）属于请求侧可自愈的行为，
	// 不产生需要告警的信号。
	Transition(from, to string)

	// AuditEmitFailed 记录一次审计事件发送失败。
	//
	// Outbox 模式下发送失败即业务事务回滚（请求已 500），该计数器直接
	// 反映「用户操作被审计故障打回」的频率——非零即应告警。
	AuditEmitFailed()
}

// noopObserver 是 Observer 的空实现；未装配指标时使用，保证零开销。
type noopObserver struct{}

// GuardDecision 空实现。
func (noopObserver) GuardDecision(string) {}

// GuardCacheHit 空实现。
func (noopObserver) GuardCacheHit(bool) {}

// Transition 空实现。
func (noopObserver) Transition(string, string) {}

// AuditEmitFailed 空实现。
func (noopObserver) AuditEmitFailed() {}

// observerOrNoop 把 nil 观测端口替换为空实现：调用方无需判空。
func observerOrNoop(o Observer) Observer {
	if o == nil {
		return noopObserver{}
	}
	return o
}

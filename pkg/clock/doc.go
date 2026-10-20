// Package clock 提供可注入的当前时间抽象，便于对 TTL、超时与业务日切做确定性测试。
//
// 设计边界：
//   - 不得依赖 internal/；本包只定义「读取当前时间」的原语，不提供调度器
//   - Clock 实现必须并发安全；Manual 内部加锁，可被多个 goroutine 共享
//   - 生产路径使用 RealClock；测试使用 NewManual 并显式推进时间
//   - 需要定时器时使用 context + time.Timer，不要把定时器塞进本包
//
// 典型用法：
//
//	type Service struct{ now clock.Clock }
//
//	func NewService(now clock.Clock) *Service {
//		if now == nil {
//			now = clock.RealClock()
//		}
//		return &Service{now: now}
//	}
package clock

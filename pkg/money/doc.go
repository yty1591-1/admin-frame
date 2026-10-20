// Package money 提供以最小货币单位表示的金额类型与分配算法。
//
// 设计边界：
//   - 不得依赖 internal/；只处理金额与币种，不包含汇率、税费、支付渠道规则
//   - 金额使用 int64 最小单位（如分），避免 float64 误差；精度由币种指数决定
//   - 不同币种禁止直接加减；换算须由调用方显式完成并处理舍入
//   - 除以汇率/比例时使用 pkg/decimal 并显式传入舍入模式
//   - 多币种账户、汇率表属于业务/基础设施层，不在本包
//
// 典型用法：
//
//	price, err := money.FromString("19.90", money.CNY)
//	total, err := price.MulInt(3)
//	parts, err := total.Split(3)
package money

// Package decimal 提供不可变的任意精度十进制定点数与舍入规则。
//
// 设计边界：
//   - 不得依赖 internal/；底层仅使用 math/big，避免 float64 的二进制误差
//   - 值表示为 unscaled / 10^scale，scale 限制在 [0, MaxScale]
//   - 所有运算返回新值，不修改接收者；可安全复制与并发读
//   - 除法必须显式传入目标 scale 与舍入模式，禁止隐式截断
//   - JSON 默认编码为字符串，避免客户端按 float64 解析造成精度丢失
//
// 典型用法：
//
//	price, err := decimal.Parse("19.90")
//	total, err := price.Mul(decimal.NewFromInt64(3))
//	rounded, err := total.Round(2, decimal.RoundHalfEven)
package decimal

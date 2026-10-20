// Package convert 提供无静默截断的类型转换与指针/零值工具。
//
// 设计边界：
//   - 不得依赖 internal/；不使用反射完成「魔法」映射
//   - 数值转换越界返回 ErrOverflow，格式非法返回 ErrInvalid，禁止静默回绕
//   - 浮点转整数采用向零截断；需要整数语义时使用 Float64ToInt64Exact
//   - 指针工具仅做薄封装，不改变 nil 语义；ValueOr 才提供默认值
//
// 典型用法：
//
//	n, err := convert.StringToInt64("42")
//	if err != nil {
//		return err
//	}
//	cfg.Timeout = convert.ValueOr(cfg.Timeout, 5*time.Second)
package convert

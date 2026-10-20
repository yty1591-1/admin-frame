package decimal

import "math/big"

// RoundingMode 指定保留 scale 时多余数字的处理方式。
//
// 零值为 RoundDown（向零截断）；财务场景应显式选择 RoundHalfEven 或业务约定模式。
type RoundingMode int

const (
	// RoundDown 向零截断（Go 整数转换语义）。
	RoundDown RoundingMode = iota
	// RoundUp 远离零方向进位。
	RoundUp
	// RoundHalfUp 四舍五入，恰好一半时远离零。
	RoundHalfUp
	// RoundHalfDown 四舍五入，恰好一半时向零。
	RoundHalfDown
	// RoundHalfEven 银行家舍入：恰好一半时取最近的偶数。
	RoundHalfEven
	// RoundFloor 向负无穷方向舍入。
	RoundFloor
	// RoundCeiling 向正无穷方向舍入。
	RoundCeiling
)

// String 返回稳定的模式名，便于日志与错误信息。
func (m RoundingMode) String() string {
	switch m {
	case RoundDown:
		return "down"
	case RoundUp:
		return "up"
	case RoundHalfUp:
		return "half-up"
	case RoundHalfDown:
		return "half-down"
	case RoundHalfEven:
		return "half-even"
	case RoundFloor:
		return "floor"
	case RoundCeiling:
		return "ceiling"
	default:
		return "unknown"
	}
}

func (m RoundingMode) valid() bool {
	return m >= RoundDown && m <= RoundCeiling
}

// roundQuotient 计算 num/den 并按 mode 舍入；den 必须为正。
func roundQuotient(num, den *big.Int, mode RoundingMode) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	sign := num.Sign()
	one := big.NewInt(1)

	switch mode {
	case RoundDown:
		return q
	case RoundUp:
		if sign > 0 {
			q.Add(q, one)
		} else {
			q.Sub(q, one)
		}
	case RoundFloor:
		if sign < 0 {
			q.Sub(q, one)
		}
	case RoundCeiling:
		if sign > 0 {
			q.Add(q, one)
		}
	case RoundHalfUp, RoundHalfDown, RoundHalfEven:
		twice := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
		cmp := twice.Cmp(den)
		inc := false
		switch mode {
		case RoundHalfUp:
			inc = cmp >= 0
		case RoundHalfDown:
			inc = cmp > 0
		case RoundHalfEven:
			inc = cmp > 0 || (cmp == 0 && q.Bit(0) == 1)
		}
		if inc {
			if sign > 0 {
				q.Add(q, one)
			} else {
				q.Sub(q, one)
			}
		}
	}
	return q
}

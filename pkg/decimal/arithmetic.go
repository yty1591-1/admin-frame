package decimal

import (
	"fmt"
	"math/big"
)

// Add 返回 d + other；结果 scale 取两者较大值。
func (d Decimal) Add(other Decimal) Decimal {
	a, b := d.aligned(other)
	scale := d.scale
	if other.scale > scale {
		scale = other.scale
	}
	var out Decimal
	out.unscaled.Add(a, b)
	out.scale = scale
	return out
}

// Sub 返回 d - other；结果 scale 取两者较大值。
func (d Decimal) Sub(other Decimal) Decimal {
	a, b := d.aligned(other)
	scale := d.scale
	if other.scale > scale {
		scale = other.scale
	}
	var out Decimal
	out.unscaled.Sub(a, b)
	out.scale = scale
	return out
}

// Mul 返回 d * other；结果 scale 为两者之和。
//
// 结果 scale 超过 MaxScale 时尝试去掉尾部零；仍超限返回 ErrScaleOverflow。
func (d Decimal) Mul(other Decimal) (Decimal, error) {
	var out Decimal
	out.unscaled.Mul(&d.unscaled, &other.unscaled)
	scale := d.scale + other.scale
	if scale > MaxScale {
		if out.unscaled.Sign() == 0 {
			scale = 0
		} else {
			excess := scale - MaxScale
			q, r := new(big.Int).QuoRem(&out.unscaled, pow10(excess), new(big.Int))
			if r.Sign() != 0 {
				return Decimal{}, fmt.Errorf("%w: result scale %d", ErrScaleOverflow, scale)
			}
			out.unscaled.Set(q)
			scale = MaxScale
		}
	}
	out.scale = scale
	return out, nil
}

// Div 返回 d / other，并按 scale 与 mode 舍入。
//
// 除数为 0 返回 ErrDivisionByZero；scale 非法返回 ErrScaleOverflow。
func (d Decimal) Div(other Decimal, scale int32, mode RoundingMode) (Decimal, error) {
	if scale < 0 || scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: scale %d", ErrScaleOverflow, scale)
	}
	if !mode.valid() {
		return Decimal{}, fmt.Errorf("%w: %d", ErrInvalidRoundingMode, mode)
	}
	if other.Sign() == 0 {
		return Decimal{}, ErrDivisionByZero
	}

	shift := other.scale + scale - d.scale
	num := new(big.Int).Set(&d.unscaled)
	den := new(big.Int).Set(&other.unscaled)
	if shift >= 0 {
		num.Mul(num, pow10(shift))
	} else {
		den.Mul(den, pow10(-shift))
	}
	if den.Sign() < 0 {
		den.Neg(den)
		num.Neg(num)
	}

	var out Decimal
	out.unscaled.Set(roundQuotient(num, den, mode))
	out.scale = scale
	return out, nil
}

// Round 将 d 舍入到 scale 位小数。
//
// scale 大于当前 scale 时补零；小于时按 mode 舍入。
func (d Decimal) Round(scale int32, mode RoundingMode) (Decimal, error) {
	if scale < 0 || scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: scale %d", ErrScaleOverflow, scale)
	}
	if !mode.valid() {
		return Decimal{}, fmt.Errorf("%w: %d", ErrInvalidRoundingMode, mode)
	}
	if scale == d.scale {
		return d, nil
	}
	if scale > d.scale {
		var out Decimal
		out.unscaled.Mul(&d.unscaled, pow10(scale-d.scale))
		out.scale = scale
		return out, nil
	}
	var out Decimal
	out.unscaled.Set(roundQuotient(&d.unscaled, pow10(d.scale-scale), mode))
	out.scale = scale
	return out, nil
}

// Truncate 向零截断到 scale 位小数。
func (d Decimal) Truncate(scale int32) (Decimal, error) {
	return d.Round(scale, RoundDown)
}

// Floor 向负无穷舍入到 scale 位小数。
func (d Decimal) Floor(scale int32) (Decimal, error) {
	return d.Round(scale, RoundFloor)
}

// Ceiling 向正无穷舍入到 scale 位小数。
func (d Decimal) Ceiling(scale int32) (Decimal, error) {
	return d.Round(scale, RoundCeiling)
}

// Neg 返回 -d。
func (d Decimal) Neg() Decimal {
	var out Decimal
	out.unscaled.Neg(&d.unscaled)
	out.scale = d.scale
	return out
}

// Abs 返回 |d|。
func (d Decimal) Abs() Decimal {
	var out Decimal
	out.unscaled.Abs(&d.unscaled)
	out.scale = d.scale
	return out
}

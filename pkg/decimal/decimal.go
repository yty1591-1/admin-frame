package decimal

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// MaxScale 是支持的最大小数位数。
const MaxScale int32 = 10000

const (
	maxDigits   = 100000
	maxExponent = 10000
)

// Decimal 是任意精度十进制定点数，值等于 Unscaled / 10^Scale。
//
// 零值为 0（scale=0）。Decimal 不可变：所有运算返回新值，
// 因此可安全复制、作为 map key 以外的值传递，并被多个 goroutine 并发读取。
type Decimal struct {
	unscaled big.Int
	scale    int32
}

// New 用 unscaled 与 scale 构造 Decimal；scale 必须在 [0, MaxScale]。
func New(unscaled int64, scale int32) (Decimal, error) {
	if scale < 0 || scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: scale %d", ErrScaleOverflow, scale)
	}
	var d Decimal
	d.unscaled.SetInt64(unscaled)
	d.scale = scale
	return d, nil
}

// MustNew 同 New，scale 非法时 panic。
func MustNew(unscaled int64, scale int32) Decimal {
	d, err := New(unscaled, scale)
	if err != nil {
		panic(err)
	}
	return d
}

// NewFromInt64 构造 scale=0 的整数 Decimal。
func NewFromInt64(v int64) Decimal {
	var d Decimal
	d.unscaled.SetInt64(v)
	return d
}

// NewFromUint64 构造 scale=0 的无符号整数 Decimal。
func NewFromUint64(v uint64) Decimal {
	var d Decimal
	d.unscaled.SetUint64(v)
	return d
}

// NewFromBigInt 复制 big.Int 构造 scale=0 的 Decimal；nil 视为 0。
func NewFromBigInt(v *big.Int) Decimal {
	var d Decimal
	if v != nil {
		d.unscaled.Set(v)
	}
	return d
}

// NewFromBigIntScale 复制 big.Int 与 scale 构造 Decimal；v 为 nil 时视为 0。
func NewFromBigIntScale(v *big.Int, scale int32) (Decimal, error) {
	if scale < 0 || scale > MaxScale {
		return Decimal{}, fmt.Errorf("%w: scale %d", ErrScaleOverflow, scale)
	}
	var d Decimal
	if v != nil {
		d.unscaled.Set(v)
	}
	d.scale = scale
	return d, nil
}

// FromFloat64 用 float64 的最短十进制表示构造 Decimal。
//
// 该函数不消除 float64 本身的二进制误差；金额等场景应优先 Parse 字符串。
func FromFloat64(v float64) (Decimal, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Decimal{}, fmt.Errorf("%w: non-finite float %v", ErrInvalid, v)
	}
	return Parse(strconv.FormatFloat(v, 'g', -1, 64))
}

// Parse 解析十进制字符串。
//
// 支持可选的 +/−、小数点与 e/E 指数，例如 "-1.25e-2"；不接受空白、千分位、
// NaN/Inf 或下划线。scale 超限返回 ErrScaleOverflow，格式非法返回 ErrInvalid。
func Parse(s string) (Decimal, error) {
	orig := s
	if s == "" {
		return Decimal{}, fmt.Errorf("%w: empty string", ErrInvalid)
	}
	if len(s) > maxDigits+16 {
		return Decimal{}, fmt.Errorf("%w: input too long", ErrInvalid)
	}

	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
	}

	exp := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		if strings.ContainsAny(s[i+1:], "eE") {
			return Decimal{}, fmt.Errorf("%w: multiple exponents", ErrInvalid)
		}
		expPart := s[i+1:]
		s = s[:i]
		if expPart == "" {
			return Decimal{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
		}
		v, err := strconv.ParseInt(expPart, 10, 64)
		if err != nil || v > maxExponent || v < -maxExponent {
			return Decimal{}, fmt.Errorf("%w: exponent %q", ErrScaleOverflow, expPart)
		}
		exp = v
	}

	fracDigits := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		if strings.IndexByte(s[i+1:], '.') >= 0 {
			return Decimal{}, fmt.Errorf("%w: multiple decimal points", ErrInvalid)
		}
		fracDigits = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	if s == "" {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
	}
	if len(s) > maxDigits {
		return Decimal{}, fmt.Errorf("%w: too many digits", ErrInvalid)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return Decimal{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
		}
	}

	u, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
	}
	if neg {
		u.Neg(u)
	}

	scale64 := int64(fracDigits) - exp
	if scale64 < 0 {
		shift := -scale64
		if shift > maxExponent {
			return Decimal{}, fmt.Errorf("%w: exponent %d", ErrScaleOverflow, exp)
		}
		u.Mul(u, pow10(int32(shift)))
		scale64 = 0
	}
	if scale64 > int64(MaxScale) {
		if u.Sign() == 0 {
			scale64 = 0
		} else {
			excess := scale64 - int64(MaxScale)
			if int64(trailingZeros(s)) < excess {
				return Decimal{}, fmt.Errorf("%w: scale %d", ErrScaleOverflow, scale64)
			}
			u.Quo(u, pow10(int32(excess)))
			scale64 = int64(MaxScale)
		}
	}

	var d Decimal
	d.unscaled.Set(u)
	d.scale = int32(scale64) //nolint:gosec // scale64 已在所有分支归一至 [0, MaxScale]。
	return d, nil
}

// MustParse 同 Parse，解析失败时 panic。
func MustParse(s string) Decimal {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func trailingZeros(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '0'; i-- {
		n++
	}
	return n
}

func pow10(n int32) *big.Int {
	if n <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// Scale 返回小数位数。
func (d Decimal) Scale() int32 { return d.scale }

// Unscaled 返回未缩放系数的副本，调用方修改不会影响 Decimal。
func (d Decimal) Unscaled() *big.Int { return new(big.Int).Set(&d.unscaled) }

// Sign 返回 -1、0 或 1。
func (d Decimal) Sign() int { return d.unscaled.Sign() }

// IsZero 判断是否为 0（忽略 scale）。
func (d Decimal) IsZero() bool { return d.unscaled.Sign() == 0 }

// IsPositive 判断是否大于 0。
func (d Decimal) IsPositive() bool { return d.unscaled.Sign() > 0 }

// IsNegative 判断是否小于 0。
func (d Decimal) IsNegative() bool { return d.unscaled.Sign() < 0 }

// IsInteger 判断小数部分是否为零。
func (d Decimal) IsInteger() bool {
	if d.scale == 0 {
		return true
	}
	_, r := new(big.Int).QuoRem(&d.unscaled, pow10(d.scale), new(big.Int))
	return r.Sign() == 0
}

// String 返回不带指数的定点字符串，并保留当前 scale。
func (d Decimal) String() string {
	if d.scale == 0 {
		return d.unscaled.String()
	}
	digits := d.unscaled.String()
	neg := strings.HasPrefix(digits, "-")
	if neg {
		digits = digits[1:]
	}
	scale := int(d.scale)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	point := len(digits) - scale
	out := digits[:point] + "." + digits[point:]
	if neg {
		out = "-" + out
	}
	return out
}

// Int64 将 Decimal 无损转为 int64。
//
// 含非零小数部分返回 ErrNotIntegral；超出 int64 返回 ErrOverflow。
func (d Decimal) Int64() (int64, error) {
	u := &d.unscaled
	if d.scale > 0 {
		q, r := new(big.Int).QuoRem(u, pow10(d.scale), new(big.Int))
		if r.Sign() != 0 {
			return 0, fmt.Errorf("%w: %s", ErrNotIntegral, d.String())
		}
		u = q
	}
	if !u.IsInt64() {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, d.String())
	}
	return u.Int64(), nil
}

// Cmp 比较 d 与 other，返回 -1、0 或 1；忽略 scale 差异。
func (d Decimal) Cmp(other Decimal) int {
	a, b := d.aligned(other)
	return a.Cmp(b)
}

// Equal 判断数值是否相等（忽略 scale）。
func (d Decimal) Equal(other Decimal) bool { return d.Cmp(other) == 0 }

// Less 判断 d 是否小于 other。
func (d Decimal) Less(other Decimal) bool { return d.Cmp(other) < 0 }

// Greater 判断 d 是否大于 other。
func (d Decimal) Greater(other Decimal) bool { return d.Cmp(other) > 0 }

func (d Decimal) aligned(other Decimal) (left, right *big.Int) {
	switch {
	case d.scale == other.scale:
		return &d.unscaled, &other.unscaled
	case d.scale > other.scale:
		return &d.unscaled, new(big.Int).Mul(&other.unscaled, pow10(d.scale-other.scale))
	default:
		return new(big.Int).Mul(&d.unscaled, pow10(other.scale-d.scale)), &other.unscaled
	}
}

package money

import (
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/decimal"
)

// Money 是带币种的金额，内部以最小货币单位（如分）的 int64 表示。
//
// 零值表示「无币种的 0」，不能参与格式化、加减或序列化；
// 需要零金额时使用 New(0, currency) 或 Zero(currency)。
type Money struct {
	amountMinor int64
	currency    Currency
}

// New 用最小货币单位构造 Money；币种必须已注册。
func New(amountMinor int64, currency Currency) (Money, error) {
	normalized := normalizeCurrency(currency)
	if _, ok := CurrencyExponent(normalized); !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}
	return Money{amountMinor: amountMinor, currency: normalized}, nil
}

// MustNew 同 New，币种非法时 panic。
func MustNew(amountMinor int64, currency Currency) Money {
	m, err := New(amountMinor, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// Zero 返回指定币种的 0 金额。
func Zero(currency Currency) (Money, error) {
	return New(0, currency)
}

// FromDecimal 将 Decimal 精确转为 Money。
//
// 金额小数位超过币种精度且含非零尾数时返回 ErrPrecision；
// 超出 int64 最小单位范围返回 ErrOverflow。
func FromDecimal(d decimal.Decimal, currency Currency) (Money, error) {
	exp, ok := CurrencyExponent(currency)
	if !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}
	scaled, err := d.Mul(decimal.NewFromInt64(minorUnits(exp)))
	if err != nil {
		return Money{}, err
	}
	minor, err := scaled.Int64()
	if err != nil {
		return Money{}, mapDecimalError(err, d)
	}
	return New(minor, currency)
}

// FromDecimalRounded 将 Decimal 按币种精度与 mode 舍入后转为 Money。
func FromDecimalRounded(d decimal.Decimal, currency Currency, mode decimal.RoundingMode) (Money, error) {
	exp, ok := CurrencyExponent(currency)
	if !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}
	scaled, err := d.Mul(decimal.NewFromInt64(minorUnits(exp)))
	if err != nil {
		return Money{}, err
	}
	rounded, err := scaled.Round(0, mode)
	if err != nil {
		return Money{}, err
	}
	minor, err := rounded.Int64()
	if err != nil {
		return Money{}, mapDecimalError(err, d)
	}
	return New(minor, currency)
}

// FromString 解析仅含数字的金额字符串（如 "19.90"），币种单独传入。
func FromString(s string, currency Currency) (Money, error) {
	d, err := decimal.Parse(s)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q: %v", ErrInvalidAmount, s, err)
	}
	return FromDecimal(d, currency)
}

// Parse 解析「币种 + 金额」文本，接受 "CNY 19.90" 或 "19.90 CNY"。
func Parse(s string) (Money, error) {
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	first, second := normalizeCurrency(Currency(parts[0])), normalizeCurrency(Currency(parts[1]))
	switch {
	case validCurrencyCode(first):
		return FromString(parts[1], first)
	case validCurrencyCode(second):
		return FromString(parts[0], second)
	default:
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
}

// AmountMinor 返回最小货币单位的整数金额。
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency 返回币种。
func (m Money) Currency() Currency { return m.currency }

// Exponent 返回币种小数位数；零值 Money 返回 (0,false)。
func (m Money) Exponent() (int, bool) { return CurrencyExponent(m.currency) }

// Add 返回 m + other；币种必须一致，溢出返回 ErrOverflow。
func (m Money) Add(other Money) (Money, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return Money{}, err
	}
	sum, ok := addInt64(m.amountMinor, other.amountMinor)
	if !ok {
		return Money{}, fmt.Errorf("%w: %d + %d", ErrOverflow, m.amountMinor, other.amountMinor)
	}
	return Money{amountMinor: sum, currency: m.currency}, nil
}

// Sub 返回 m - other；币种必须一致，溢出返回 ErrOverflow。
func (m Money) Sub(other Money) (Money, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return Money{}, err
	}
	diff, ok := subInt64(m.amountMinor, other.amountMinor)
	if !ok {
		return Money{}, fmt.Errorf("%w: %d - %d", ErrOverflow, m.amountMinor, other.amountMinor)
	}
	return Money{amountMinor: diff, currency: m.currency}, nil
}

// Neg 返回 -m；最小 int64 无法取反，返回 ErrOverflow。
func (m Money) Neg() (Money, error) {
	if m.amountMinor == minInt64 {
		return Money{}, fmt.Errorf("%w: negate %d", ErrOverflow, m.amountMinor)
	}
	return Money{amountMinor: -m.amountMinor, currency: m.currency}, nil
}

// Abs 返回 |m|；最小 int64 无法取绝对值，返回 ErrOverflow。
func (m Money) Abs() (Money, error) {
	if m.amountMinor == minInt64 {
		return Money{}, fmt.Errorf("%w: abs %d", ErrOverflow, m.amountMinor)
	}
	if m.amountMinor < 0 {
		return Money{amountMinor: -m.amountMinor, currency: m.currency}, nil
	}
	return m, nil
}

// MulInt 返回 m * n；溢出返回 ErrOverflow。
func (m Money) MulInt(n int64) (Money, error) {
	if _, ok := CurrencyExponent(m.currency); !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	product, ok := mulInt64(m.amountMinor, n)
	if !ok {
		return Money{}, fmt.Errorf("%w: %d * %d", ErrOverflow, m.amountMinor, n)
	}
	return Money{amountMinor: product, currency: m.currency}, nil
}

// MulDecimal 将金额乘以比例并按币种精度舍入，例如 1000 分 * 1.08 = 1080 分。
//
// rate 为无量纲比例；mode 必须合法，否则返回 decimal.ErrInvalidRoundingMode。
func (m Money) MulDecimal(rate decimal.Decimal, mode decimal.RoundingMode) (Money, error) {
	if _, ok := CurrencyExponent(m.currency); !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	product, err := decimal.NewFromInt64(m.amountMinor).Mul(rate)
	if err != nil {
		return Money{}, err
	}
	rounded, err := product.Round(0, mode)
	if err != nil {
		return Money{}, err
	}
	minor, err := rounded.Int64()
	if err != nil {
		return Money{}, mapDecimalError(err, rate)
	}
	return Money{amountMinor: minor, currency: m.currency}, nil
}

// DivDecimal 将金额除以比例并按币种精度舍入；除数为 0 返回 decimal.ErrDivisionByZero。
func (m Money) DivDecimal(divisor decimal.Decimal, mode decimal.RoundingMode) (Money, error) {
	if _, ok := CurrencyExponent(m.currency); !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	quotient, err := decimal.NewFromInt64(m.amountMinor).Div(divisor, 0, mode)
	if err != nil {
		return Money{}, err
	}
	minor, err := quotient.Int64()
	if err != nil {
		return Money{}, mapDecimalError(err, divisor)
	}
	return Money{amountMinor: minor, currency: m.currency}, nil
}

// Cmp 比较金额，返回 -1、0 或 1；币种不一致返回 ErrCurrencyMismatch。
func (m Money) Cmp(other Money) (int, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return 0, err
	}
	switch {
	case m.amountMinor < other.amountMinor:
		return -1, nil
	case m.amountMinor > other.amountMinor:
		return 1, nil
	default:
		return 0, nil
	}
}

// Equal 判断币种与最小单位金额是否完全一致；零值 Money 与自身相等。
func (m Money) Equal(other Money) bool {
	return m.currency == other.currency && m.amountMinor == other.amountMinor
}

// IsZero 判断金额是否为 0（不校验币种）。
func (m Money) IsZero() bool { return m.amountMinor == 0 }

// Sign 返回 -1、0 或 1。
func (m Money) Sign() int {
	switch {
	case m.amountMinor < 0:
		return -1
	case m.amountMinor > 0:
		return 1
	default:
		return 0
	}
}

// Format 返回不含币种的定点金额，小数位由币种指数决定。
func (m Money) Format() string {
	exp, ok := CurrencyExponent(m.currency)
	if !ok {
		exp = 0
	}
	return formatMinor(m.amountMinor, exp)
}

// String 返回 "CNY 19.90" 形式；零值 Money 仅返回数字。
func (m Money) String() string {
	if m.currency == "" {
		return m.Format()
	}
	return string(m.currency) + " " + m.Format()
}

func (m Money) ensureSameCurrency(other Money) error {
	if m.currency != other.currency {
		return fmt.Errorf("%w: %q vs %q", ErrCurrencyMismatch, m.currency, other.currency)
	}
	if _, ok := CurrencyExponent(m.currency); !ok {
		return fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	return nil
}

func mapDecimalError(err error, value decimal.Decimal) error {
	switch {
	case stderrors.Is(err, decimal.ErrNotIntegral):
		return fmt.Errorf("%w: %s", ErrPrecision, value)
	case stderrors.Is(err, decimal.ErrOverflow):
		return fmt.Errorf("%w: %s", ErrOverflow, value)
	default:
		return err
	}
}

func formatMinor(amount int64, exponent int) string {
	digits := strconv.FormatInt(amount, 10)
	neg := strings.HasPrefix(digits, "-")
	if neg {
		digits = digits[1:]
	}
	if exponent > 0 {
		if len(digits) <= exponent {
			digits = strings.Repeat("0", exponent-len(digits)+1) + digits
		}
		point := len(digits) - exponent
		digits = digits[:point] + "." + digits[point:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

const minInt64 = -1 << 63

func addInt64(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

func subInt64(a, b int64) (int64, bool) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, false
	}
	return diff, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if (a == -1 && b == minInt64) || (b == -1 && a == minInt64) {
		return 0, false
	}
	product := a * b
	if product/b != a {
		return 0, false
	}
	return product, true
}

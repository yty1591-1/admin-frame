package money

import (
	"fmt"
	"strings"
	"sync"
)

// Currency 是 ISO 4217 风格的 3 位大写币种代码。
type Currency string

const (
	// CNY 人民币，2 位小数。
	CNY Currency = "CNY"
	// USD 美元，2 位小数。
	USD Currency = "USD"
	// EUR 欧元，2 位小数。
	EUR Currency = "EUR"
	// GBP 英镑，2 位小数。
	GBP Currency = "GBP"
	// JPY 日元，0 位小数。
	JPY Currency = "JPY"
	// KRW 韩元，0 位小数。
	KRW Currency = "KRW"
	// HKD 港币，2 位小数。
	HKD Currency = "HKD"
	// SGD 新加坡元，2 位小数。
	SGD Currency = "SGD"
	// AUD 澳元，2 位小数。
	AUD Currency = "AUD"
	// CAD 加元，2 位小数。
	CAD Currency = "CAD"
	// CHF 瑞士法郎，2 位小数。
	CHF Currency = "CHF"
	// TWD 新台币，2 位小数。
	TWD Currency = "TWD"
)

var (
	currencyMu sync.RWMutex
	// currencyExponents 保存已注册币种的指数；仅在初始化或显式注册时写入。
	currencyExponents = map[Currency]int{
		CNY: 2, USD: 2, EUR: 2, GBP: 2,
		JPY: 0, KRW: 0, "VND": 0,
		HKD: 2, SGD: 2, AUD: 2, CAD: 2, CHF: 2, TWD: 2,
		"INR": 2, "BRL": 2, "RUB": 2, "THB": 2,
		"KWD": 3, "BHD": 3, "OMR": 3, "JOD": 3, "TND": 3, "IQD": 3, "LYD": 3,
	}
)

// RegisterCurrency 注册或校验币种指数。
//
// code 必须为 3 位 ASCII 字母（大小写均可，内部统一大写），exponent 必须在 [0,9]。
// 已注册且指数一致时返回 nil；指数冲突返回 ErrCurrencyConflict。
// 该函数并发安全，建议仅在进程初始化阶段调用。
func RegisterCurrency(code Currency, exponent int) error {
	normalized := normalizeCurrency(code)
	if !validCurrencyCode(normalized) || exponent < 0 || exponent > 9 {
		return fmt.Errorf("%w: %q exponent=%d", ErrInvalidCurrency, code, exponent)
	}
	currencyMu.Lock()
	defer currencyMu.Unlock()
	if existing, ok := currencyExponents[normalized]; ok {
		if existing != exponent {
			return fmt.Errorf("%w: %s existing=%d new=%d", ErrCurrencyConflict, normalized, existing, exponent)
		}
		return nil
	}
	currencyExponents[normalized] = exponent
	return nil
}

// CurrencyExponent 返回币种的小数位数；未注册时 ok 为 false。
func CurrencyExponent(code Currency) (int, bool) {
	currencyMu.RLock()
	defer currencyMu.RUnlock()
	exp, ok := currencyExponents[normalizeCurrency(code)]
	return exp, ok
}

// Valid 判断币种是否已注册。
func (c Currency) Valid() bool {
	_, ok := CurrencyExponent(c)
	return ok
}

func normalizeCurrency(c Currency) Currency {
	return Currency(strings.ToUpper(string(c)))
}

func validCurrencyCode(c Currency) bool {
	s := string(c)
	if len(s) != 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// minorUnits 返回 1 个主单位对应的最小单位数；exponent 已限制在 [0,9]。
func minorUnits(exponent int) int64 {
	return [...]int64{1, 10, 100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000, 1_000_000_000}[exponent]
}

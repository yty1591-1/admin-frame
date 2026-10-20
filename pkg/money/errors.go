package money

import stderrors "errors"

var (
	// ErrUnknownCurrency 表示币种未注册。
	ErrUnknownCurrency = stderrors.New("money: unknown currency")
	// ErrCurrencyMismatch 表示对不同币种做加减或比较。
	ErrCurrencyMismatch = stderrors.New("money: currency mismatch")
	// ErrCurrencyConflict 表示重复注册币种但指数不一致。
	ErrCurrencyConflict = stderrors.New("money: currency exponent conflict")
	// ErrInvalidCurrency 表示币种代码不是 3 位字母或指数非法。
	ErrInvalidCurrency = stderrors.New("money: invalid currency")
	// ErrInvalidAmount 表示金额字符串或分配权重非法。
	ErrInvalidAmount = stderrors.New("money: invalid amount")
	// ErrPrecision 表示金额精度超过币种最小单位。
	ErrPrecision = stderrors.New("money: amount exceeds currency precision")
	// ErrOverflow 表示金额运算溢出 int64。
	ErrOverflow = stderrors.New("money: amount overflow")
	// ErrInvalidAllocation 表示分配权重为空、全零或为负。
	ErrInvalidAllocation = stderrors.New("money: invalid allocation")
)

package decimal

import stderrors "errors"

var (
	// ErrInvalid 表示字符串或输入格式非法。
	ErrInvalid = stderrors.New("decimal: invalid value")
	// ErrScaleOverflow 表示 scale 超出 [0, MaxScale] 或运算后精度过高。
	ErrScaleOverflow = stderrors.New("decimal: scale out of range")
	// ErrDivisionByZero 表示除数为 0。
	ErrDivisionByZero = stderrors.New("decimal: division by zero")
	// ErrNotIntegral 表示值含非零小数部分，无法无损转为整数。
	ErrNotIntegral = stderrors.New("decimal: not an integer")
	// ErrOverflow 表示值超出目标整数类型范围。
	ErrOverflow = stderrors.New("decimal: overflow")
	// ErrInvalidRoundingMode 表示舍入模式非法。
	ErrInvalidRoundingMode = stderrors.New("decimal: invalid rounding mode")
)

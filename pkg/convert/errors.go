package convert

import stderrors "errors"

var (
	// ErrInvalid 表示输入格式或类型不受支持。
	ErrInvalid = stderrors.New("convert: invalid value")
	// ErrOverflow 表示数值超出目标类型可表示范围。
	ErrOverflow = stderrors.New("convert: value out of range")
)

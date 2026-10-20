package convert

import (
	"fmt"
	"math"
	"strconv"
)

// StringToInt 解析十进制整数；空白、前缀或越界返回 ErrInvalid。
func StringToInt(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%w: int %q", ErrInvalid, s)
	}
	return v, nil
}

// StringToInt64 解析十进制 int64；格式非法或越界返回 ErrInvalid。
func StringToInt64(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: int64 %q", ErrInvalid, s)
	}
	return v, nil
}

// StringToUint64 解析十进制 uint64；负数、格式非法或越界返回 ErrInvalid。
func StringToUint64(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: uint64 %q", ErrInvalid, s)
	}
	return v, nil
}

// StringToFloat64 解析 float64；NaN、±Inf 与格式非法均返回 ErrInvalid。
func StringToFloat64(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: float64 %q", ErrInvalid, s)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%w: non-finite float64 %q", ErrInvalid, s)
	}
	return v, nil
}

// StringToBool 按 strconv.ParseBool 规则解析布尔值。
//
// 接受 1/t/T/TRUE/true/True 与 0/f/F/FALSE/false/False；其他输入返回 ErrInvalid。
func StringToBool(s string) (bool, error) {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("%w: bool %q", ErrInvalid, s)
	}
	return v, nil
}

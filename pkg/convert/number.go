package convert

import (
	"fmt"
	"math"
	"strconv"
)

// Int64ToInt 将 int64 转为 int；32 位平台越界返回 ErrOverflow。
func Int64ToInt(v int64) (int, error) {
	if strconv.IntSize == 32 && (v < math.MinInt32 || v > math.MaxInt32) {
		return 0, fmt.Errorf("%w: %d overflows int", ErrOverflow, v)
	}
	return int(v), nil
}

// Int64ToInt32 将 int64 转为 int32；越界返回 ErrOverflow。
func Int64ToInt32(v int64) (int32, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %d overflows int32", ErrOverflow, v)
	}
	return int32(v), nil
}

// Uint64ToInt64 将 uint64 转为 int64；超过 MaxInt64 返回 ErrOverflow。
func Uint64ToInt64(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("%w: %d overflows int64", ErrOverflow, v)
	}
	return int64(v), nil
}

// Uint64ToInt 将 uint64 转为 int；越界返回 ErrOverflow。
func Uint64ToInt(v uint64) (int, error) {
	if v > math.MaxInt {
		return 0, fmt.Errorf("%w: %d overflows int", ErrOverflow, v)
	}
	return int(v), nil
}

// Int64ToUint64 将 int64 转为 uint64；负数返回 ErrOverflow。
func Int64ToUint64(v int64) (uint64, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: %d is negative", ErrOverflow, v)
	}
	return uint64(v), nil
}

// Float64ToInt64 将 float64 向零截断为 int64。
//
// NaN、±Inf 与超出 int64 范围的值返回 ErrOverflow；小数部分被丢弃。
// 需要「必须为整数」语义时使用 Float64ToInt64Exact。
func Float64ToInt64(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= float64(1<<63) || v < -float64(1<<63) {
		return 0, fmt.Errorf("%w: %v overflows int64", ErrOverflow, v)
	}
	return int64(v), nil
}

// Float64ToInt64Exact 将 float64 转为 int64，且要求没有小数部分。
//
// 非整数返回 ErrInvalid；NaN、±Inf 或越界返回 ErrOverflow。
func Float64ToInt64Exact(v float64) (int64, error) {
	i, err := Float64ToInt64(v)
	if err != nil {
		return 0, err
	}
	if float64(i) != v {
		return 0, fmt.Errorf("%w: %v is not an integer", ErrInvalid, v)
	}
	return i, nil
}

// Float64ToInt 将 float64 向零截断为 int；越界返回 ErrOverflow。
func Float64ToInt(v float64) (int, error) {
	i, err := Float64ToInt64(v)
	if err != nil {
		return 0, err
	}
	return Int64ToInt(i)
}

// Float64ToFloat32 将 float64 转为 float32。
//
// 有限值超过 float32 上限时返回 ErrOverflow；NaN/±Inf 原样保留。
// 该转换可能损失精度，调用方须自行评估。
func Float64ToFloat32(v float64) (float32, error) {
	if !math.IsNaN(v) && !math.IsInf(v, 0) && (v > math.MaxFloat32 || v < -math.MaxFloat32) {
		return 0, fmt.Errorf("%w: %v overflows float32", ErrOverflow, v)
	}
	return float32(v), nil
}

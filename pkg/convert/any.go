package convert

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// ToString 将常见标量安全转为字符串。
//
// 支持 string、[]byte、bool、整数、浮点、json.Number 与 fmt.Stringer；
// 不支持的类型返回 ErrInvalid。该函数不会用 fmt.Sprint 兜底字符串化结构体。
func ToString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("%w: nil", ErrInvalid)
	case string:
		return x, nil
	case []byte:
		return string(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case fmt.Stringer:
		return x.String(), nil
	default:
		return "", fmt.Errorf("%w: unsupported type %T", ErrInvalid, v)
	}
}

// ToInt64 将常见标量安全转为 int64。
//
// 支持整数、整数值浮点、string、[]byte 与 json.Number；
// 小数、负数转无符号、越界或不支持的类型分别返回 ErrInvalid 或 ErrOverflow。
func ToInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return Uint64ToInt64(uint64(x))
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return Uint64ToInt64(x)
	case float32:
		return Float64ToInt64Exact(float64(x))
	case float64:
		return Float64ToInt64Exact(x)
	case string:
		return StringToInt64(x)
	case []byte:
		return StringToInt64(string(x))
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		f, err := StringToFloat64(x.String())
		if err != nil {
			return 0, err
		}
		return Float64ToInt64Exact(f)
	default:
		return 0, fmt.Errorf("%w: unsupported type %T", ErrInvalid, v)
	}
}

// ToFloat64 将常见标量安全转为 float64。
//
// 支持整数、浮点、string、[]byte 与 json.Number；NaN 与 ±Inf 返回 ErrInvalid。
// 大整数（> 2^53）转换可能损失精度，属调用方责任。
func ToFloat64(v any) (float64, error) {
	var f float64
	switch x := v.(type) {
	case int:
		f = float64(x)
	case int8:
		f = float64(x)
	case int16:
		f = float64(x)
	case int32:
		f = float64(x)
	case int64:
		f = float64(x)
	case uint:
		f = float64(x)
	case uint8:
		f = float64(x)
	case uint16:
		f = float64(x)
	case uint32:
		f = float64(x)
	case uint64:
		f = float64(x)
	case float32:
		f = float64(x)
	case float64:
		f = x
	case string:
		return StringToFloat64(x)
	case []byte:
		return StringToFloat64(string(x))
	case json.Number:
		return StringToFloat64(x.String())
	default:
		return 0, fmt.Errorf("%w: unsupported type %T", ErrInvalid, v)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: non-finite float", ErrInvalid)
	}
	return f, nil
}

// ToBool 将常见标量安全转为 bool。
//
// 支持 bool、string、[]byte、json.Number，以及取值为 0/1 的整数；
// 其他值返回 ErrInvalid。
func ToBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		return StringToBool(x)
	case []byte:
		return StringToBool(string(x))
	case json.Number:
		return StringToBool(x.String())
	case int:
		return intZeroOne(int64(x))
	case int8:
		return intZeroOne(int64(x))
	case int16:
		return intZeroOne(int64(x))
	case int32:
		return intZeroOne(int64(x))
	case int64:
		return intZeroOne(x)
	case uint:
		return uintZeroOne(uint64(x))
	case uint8:
		return uintZeroOne(uint64(x))
	case uint16:
		return uintZeroOne(uint64(x))
	case uint32:
		return uintZeroOne(uint64(x))
	case uint64:
		return uintZeroOne(x)
	default:
		return false, fmt.Errorf("%w: unsupported type %T", ErrInvalid, v)
	}
}

func intZeroOne(v int64) (bool, error) {
	switch v {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: bool from integer %d", ErrInvalid, v)
	}
}

func uintZeroOne(v uint64) (bool, error) {
	switch v {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: bool from integer %d", ErrInvalid, v)
	}
}

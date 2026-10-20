package decimal

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MarshalText 将 Decimal 编码为定点字符串。
func (d Decimal) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText 从定点字符串解析。
func (d *Decimal) UnmarshalText(b []byte) error {
	if d == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalid)
	}
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalJSON 将 Decimal 编码为 JSON 字符串，避免客户端按 float64 解析丢失精度。
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(d.String())), nil
}

// UnmarshalJSON 接受 JSON 字符串或数字字面量；null 解码为零值。
func (d *Decimal) UnmarshalJSON(b []byte) error {
	if d == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalid)
	}
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*d = Decimal{}
		return nil
	}
	if s == "" {
		return fmt.Errorf("%w: empty JSON", ErrInvalid)
	}
	if s[0] == '"' {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		s = text
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Value 实现 driver.Valuer，以定点字符串写入数据库，避免 float64 精度损失。
func (d Decimal) Value() (driver.Value, error) { return d.String(), nil }

// Scan 实现 sql.Scanner，支持 nil、string、[]byte、int64 与 float64。
//
// float64 按其最短十进制表示解析；数据库 DECIMAL 应优先返回字符串/[]byte。
func (d *Decimal) Scan(src any) error {
	if d == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalid)
	}
	switch v := src.(type) {
	case nil:
		*d = Decimal{}
		return nil
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		parsed, err := Parse(string(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case int64:
		*d = NewFromInt64(v)
		return nil
	case float64:
		parsed, err := FromFloat64(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		return fmt.Errorf("%w: unsupported SQL type %T", ErrInvalid, src)
	}
}

// 编译期断言。
var (
	_ driver.Valuer = Decimal{}
	_ fmt.Stringer  = Decimal{}
)

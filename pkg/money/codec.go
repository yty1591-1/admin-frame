package money

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/decimal"
)

// MarshalText 将 Money 编码为 "CNY 19.90" 文本。
func (m Money) MarshalText() ([]byte, error) {
	if _, ok := CurrencyExponent(m.currency); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	return []byte(m.String()), nil
}

// UnmarshalText 从 "CNY 19.90" 或 "19.90 CNY" 解析。
func (m *Money) UnmarshalText(b []byte) error {
	if m == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalidAmount)
	}
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

type moneyJSON struct {
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
}

// MarshalJSON 编码为 {"amount":"19.90","currency":"CNY"}，金额不使用 JSON number。
func (m Money) MarshalJSON() ([]byte, error) {
	exp, ok := CurrencyExponent(m.currency)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	return json.Marshal(moneyJSON{Amount: formatMinor(m.amountMinor, exp), Currency: m.currency})
}

// UnmarshalJSON 接受对象 {"amount":"19.90","currency":"CNY"}、
// 文本 "CNY 19.90" 或 null（零值）。
func (m *Money) UnmarshalJSON(b []byte) error {
	if m == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalidAmount)
	}
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*m = Money{}
		return nil
	}
	if s == "" {
		return fmt.Errorf("%w: empty JSON", ErrInvalidAmount)
	}
	if s[0] == '"' {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
		}
		parsed, err := Parse(text)
		if err != nil {
			return err
		}
		*m = parsed
		return nil
	}

	var raw struct {
		Amount   json.RawMessage `json:"amount"`
		Currency Currency        `json:"currency"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if len(raw.Amount) == 0 {
		return fmt.Errorf("%w: missing amount", ErrInvalidAmount)
	}
	amount := string(raw.Amount)
	if raw.Amount[0] == '"' {
		if err := json.Unmarshal(raw.Amount, &amount); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
		}
	}
	d, err := decimal.Parse(amount)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrInvalidAmount, amount, err)
	}
	parsed, err := FromDecimal(d, raw.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

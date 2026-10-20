package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/decimal"
	"github.com/admin-frame/admin-frame/pkg/money"
)

func TestNewAndFormat(t *testing.T) {
	tests := []struct {
		name   string
		minor  int64
		cur    money.Currency
		format string
		text   string
	}{
		{name: "cny", minor: 1990, cur: money.CNY, format: "19.90", text: "CNY 19.90"},
		{name: "jpy", minor: 1990, cur: money.JPY, format: "1990", text: "JPY 1990"},
		{name: "kwd", minor: 1234, cur: money.Currency("KWD"), format: "1.234", text: "KWD 1.234"},
		{name: "negative", minor: -5, cur: money.CNY, format: "-0.05", text: "CNY -0.05"},
		{name: "zero", minor: 0, cur: money.CNY, format: "0.00", text: "CNY 0.00"},
		{name: "min int64", minor: math.MinInt64, cur: money.CNY, format: "-92233720368547758.08", text: "CNY -92233720368547758.08"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.New(tt.minor, tt.cur)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Format(); got != tt.format {
				t.Fatalf("format=%q want=%q", got, tt.format)
			}
			if got := m.String(); got != tt.text {
				t.Fatalf("string=%q want=%q", got, tt.text)
			}
			if m.AmountMinor() != tt.minor {
				t.Fatalf("minor=%d", m.AmountMinor())
			}
		})
	}
}

func TestNewUnknownCurrency(t *testing.T) {
	if _, err := money.New(1, "ZZZ"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
	if _, err := money.New(1, ""); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
}

func TestCurrencyNormalization(t *testing.T) {
	m, err := money.New(100, money.Currency("usd"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Currency() != money.USD {
		t.Fatalf("currency=%s", m.Currency())
	}
}

func TestFromStringAndDecimal(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		cur     money.Currency
		minor   int64
		wantErr error
	}{
		{name: "exact", in: "19.90", cur: money.CNY, minor: 1990},
		{name: "short scale", in: "19.9", cur: money.CNY, minor: 1990},
		{name: "jpy", in: "1990", cur: money.JPY, minor: 1990},
		{name: "kwd", in: "1.234", cur: money.Currency("KWD"), minor: 1234},
		{name: "too precise", in: "19.905", cur: money.CNY, wantErr: money.ErrPrecision},
		{name: "trailing zeros ok", in: "19.900", cur: money.CNY, minor: 1990},
		{name: "invalid", in: "abc", cur: money.CNY, wantErr: money.ErrInvalidAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.FromString(tt.in, tt.cur)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want=%v", err, tt.wantErr)
				}
				return
			}
			if err != nil || m.AmountMinor() != tt.minor {
				t.Fatalf("minor=%d err=%v want=%d", m.AmountMinor(), err, tt.minor)
			}
		})
	}

	if _, err := money.FromDecimal(decimal.MustParse("1.005"), money.USD); !errors.Is(err, money.ErrPrecision) {
		t.Fatalf("err=%v", err)
	}
	m, err := money.FromDecimalRounded(decimal.MustParse("1.005"), money.USD, decimal.RoundHalfUp)
	if err != nil || m.AmountMinor() != 101 {
		t.Fatalf("minor=%d err=%v", m.AmountMinor(), err)
	}
	if _, err := money.FromDecimal(decimal.MustParse("1"), "ZZZ"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in    string
		minor int64
		cur   money.Currency
	}{
		{in: "CNY 19.90", minor: 1990, cur: money.CNY},
		{in: "19.90 CNY", minor: 1990, cur: money.CNY},
		{in: "usd 1.00", minor: 100, cur: money.USD},
	}
	for _, tt := range tests {
		m, err := money.Parse(tt.in)
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if m.AmountMinor() != tt.minor || m.Currency() != tt.cur {
			t.Fatalf("%q -> %s", tt.in, m)
		}
	}
	for _, s := range []string{"", "CNY", "CNY 1.00 extra", "ZZZ 1.00"} {
		if _, err := money.Parse(s); err == nil {
			t.Fatalf("%q: expected error", s)
		}
	}
}

func TestAddSub(t *testing.T) {
	a := money.MustNew(1000, money.CNY)
	b := money.MustNew(250, money.CNY)
	sum, err := a.Add(b)
	if err != nil || sum.AmountMinor() != 1250 {
		t.Fatalf("sum=%s err=%v", sum, err)
	}
	diff, err := a.Sub(b)
	if err != nil || diff.AmountMinor() != 750 {
		t.Fatalf("diff=%s err=%v", diff, err)
	}
	if _, err := a.Add(money.MustNew(1, money.USD)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("err=%v", err)
	}
	if _, err := money.MustNew(math.MaxInt64, money.CNY).Add(money.MustNew(1, money.CNY)); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
	if _, err := money.MustNew(math.MinInt64, money.CNY).Sub(money.MustNew(1, money.CNY)); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
}

func TestNegAbsMul(t *testing.T) {
	if got, err := money.MustNew(-5, money.CNY).Neg(); err != nil || got.AmountMinor() != 5 {
		t.Fatalf("neg=%s err=%v", got, err)
	}
	if _, err := money.MustNew(math.MinInt64, money.CNY).Neg(); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
	if got, err := money.MustNew(-5, money.CNY).Abs(); err != nil || got.AmountMinor() != 5 {
		t.Fatalf("abs=%s err=%v", got, err)
	}
	if _, err := money.MustNew(math.MinInt64, money.CNY).Abs(); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
	if got, err := money.MustNew(1000, money.CNY).MulInt(3); err != nil || got.AmountMinor() != 3000 {
		t.Fatalf("mul=%s err=%v", got, err)
	}
	if _, err := money.MustNew(math.MaxInt64, money.CNY).MulInt(2); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
	if _, err := money.MustNew(math.MinInt64, money.CNY).MulInt(-1); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecimalArithmetic(t *testing.T) {
	m := money.MustNew(1000, money.CNY)
	got, err := m.MulDecimal(decimal.MustParse("1.08"), decimal.RoundHalfEven)
	if err != nil || got.AmountMinor() != 1080 {
		t.Fatalf("mul=%s err=%v", got, err)
	}
	if got, err := m.MulDecimal(decimal.MustParse("0.0005"), decimal.RoundHalfUp); err != nil || got.AmountMinor() != 1 {
		t.Fatalf("half up=%s err=%v", got, err)
	}
	if got, err := m.MulDecimal(decimal.MustParse("0.0005"), decimal.RoundHalfEven); err != nil || got.AmountMinor() != 0 {
		t.Fatalf("half even=%s err=%v", got, err)
	}
	if _, err := m.MulDecimal(decimal.MustParse("1"), decimal.RoundingMode(99)); !errors.Is(err, decimal.ErrInvalidRoundingMode) {
		t.Fatalf("err=%v", err)
	}

	if got, err := m.DivDecimal(decimal.NewFromInt64(3), decimal.RoundHalfUp); err != nil || got.AmountMinor() != 333 {
		t.Fatalf("div=%s err=%v", got, err)
	}
	if _, err := m.DivDecimal(decimal.Decimal{}, decimal.RoundDown); !errors.Is(err, decimal.ErrDivisionByZero) {
		t.Fatalf("err=%v", err)
	}
	if _, err := (money.Money{}).MulInt(2); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
}

func TestCompare(t *testing.T) {
	a := money.MustNew(100, money.CNY)
	b := money.MustNew(100, money.CNY)
	if !a.Equal(b) || a.IsZero() {
		t.Fatalf("a=%s b=%s", a, b)
	}
	if cmp, err := a.Cmp(b); err != nil || cmp != 0 {
		t.Fatalf("cmp=%d err=%v", cmp, err)
	}
	if cmp, err := a.Cmp(money.MustNew(101, money.CNY)); err != nil || cmp != -1 {
		t.Fatalf("cmp=%d err=%v", cmp, err)
	}
	if _, err := a.Cmp(money.MustNew(100, money.USD)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("err=%v", err)
	}
	if money.MustNew(-1, money.CNY).Sign() != -1 || money.MustNew(0, money.CNY).Sign() != 0 {
		t.Fatal("sign mismatch")
	}
}

func TestAllocate(t *testing.T) {
	m := money.MustNew(100, money.CNY)
	parts, err := m.Allocate(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{34, 33, 33}
	if len(parts) != len(want) {
		t.Fatalf("len=%d", len(parts))
	}
	var sum int64
	for i, p := range parts {
		if p.AmountMinor() != want[i] {
			t.Fatalf("parts=%v want=%v", parts, want)
		}
		sum += p.AmountMinor()
	}
	if sum != 100 {
		t.Fatalf("sum=%d", sum)
	}

	parts, err = m.Allocate(1, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	want = []int64{17, 33, 50}
	sum = 0
	for i, p := range parts {
		if p.AmountMinor() != want[i] {
			t.Fatalf("parts=%v want=%v", parts, want)
		}
		sum += p.AmountMinor()
	}
	if sum != 100 {
		t.Fatalf("sum=%d", sum)
	}

	negative, err := money.MustNew(-100, money.CNY).Allocate(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if negative[0].AmountMinor() != -34 || negative[1].AmountMinor() != -33 || negative[2].AmountMinor() != -33 {
		t.Fatalf("negative=%v", negative)
	}

	zero, err := money.MustNew(0, money.CNY).Split(3)
	if err != nil || len(zero) != 3 {
		t.Fatalf("zero=%v err=%v", zero, err)
	}
	for _, p := range zero {
		if !p.IsZero() || p.Currency() != money.CNY {
			t.Fatalf("zero part=%s", p)
		}
	}

	if _, err := m.Allocate(0, 0); !errors.Is(err, money.ErrInvalidAllocation) {
		t.Fatalf("err=%v", err)
	}
	if _, err := (money.Money{}).Allocate(1); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
	if _, err := m.Allocate(); !errors.Is(err, money.ErrInvalidAllocation) {
		t.Fatalf("err=%v", err)
	}
	if _, err := m.Allocate(-1, 2); !errors.Is(err, money.ErrInvalidAllocation) {
		t.Fatalf("err=%v", err)
	}
	if _, err := m.Split(0); !errors.Is(err, money.ErrInvalidAllocation) {
		t.Fatalf("err=%v", err)
	}
}

func TestSplitMinInt64(t *testing.T) {
	parts, err := money.MustNew(math.MinInt64, money.CNY).Split(2)
	if err != nil {
		t.Fatal(err)
	}
	if parts[0].AmountMinor()+parts[1].AmountMinor() != math.MinInt64 {
		t.Fatalf("parts=%v", parts)
	}
}

func TestJSON(t *testing.T) {
	m := money.MustNew(1990, money.CNY)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"amount":"19.90","currency":"CNY"}` {
		t.Fatalf("json=%s", b)
	}

	var got money.Money
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(m) {
		t.Fatalf("got=%s", got)
	}
	if err := json.Unmarshal([]byte(`"19.90 CNY"`), &got); err != nil || !got.Equal(m) {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if err := json.Unmarshal([]byte(`{"amount":19.90,"currency":"CNY"}`), &got); err != nil || !got.Equal(m) {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if err := json.Unmarshal([]byte(`null`), &got); err != nil || !got.Equal(money.Money{}) {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if err := json.Unmarshal([]byte(`{"amount":"1.00"}`), &got); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
	if _, err := json.Marshal(money.Money{}); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("err=%v", err)
	}
}

func TestText(t *testing.T) {
	m := money.MustNew(1990, money.CNY)
	b, err := m.MarshalText()
	if err != nil || string(b) != "CNY 19.90" {
		t.Fatalf("text=%s err=%v", b, err)
	}
	var got money.Money
	if err := got.UnmarshalText(b); err != nil || !got.Equal(m) {
		t.Fatalf("got=%s err=%v", got, err)
	}
}

func TestRegisterCurrency(t *testing.T) {
	if err := money.RegisterCurrency("xau", 4); err != nil {
		t.Fatal(err)
	}
	if exp, ok := money.CurrencyExponent("XAU"); !ok || exp != 4 {
		t.Fatalf("exp=%d ok=%v", exp, ok)
	}
	if err := money.RegisterCurrency("XAU", 4); err != nil {
		t.Fatal(err)
	}
	if err := money.RegisterCurrency(money.CNY, 3); !errors.Is(err, money.ErrCurrencyConflict) {
		t.Fatalf("err=%v", err)
	}
	if err := money.RegisterCurrency("US", 2); !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("err=%v", err)
	}
	if err := money.RegisterCurrency("ABC", 10); !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("err=%v", err)
	}
	if !money.CNY.Valid() || money.Currency("ZZZ").Valid() {
		t.Fatal("Valid mismatch")
	}
}

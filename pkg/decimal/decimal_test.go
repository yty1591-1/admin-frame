package decimal_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/decimal"
)

func TestParseAndString(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantScale int32
	}{
		{name: "zero", in: "0", want: "0", wantScale: 0},
		{name: "integer", in: "-12", want: "-12", wantScale: 0},
		{name: "fraction", in: "1.20", want: "1.20", wantScale: 2},
		{name: "leading dot", in: ".5", want: "0.5", wantScale: 1},
		{name: "trailing dot", in: "1.", want: "1", wantScale: 0},
		{name: "plus sign", in: "+1.25", want: "1.25", wantScale: 2},
		{name: "leading zeros", in: "001.2300", want: "1.2300", wantScale: 4},
		{name: "positive exponent", in: "1e3", want: "1000", wantScale: 0},
		{name: "negative exponent", in: "1.25e-2", want: "0.0125", wantScale: 4},
		{name: "negative zero", in: "-0.00", want: "0.00", wantScale: 2},
		{name: "zero padded", in: "0.000", want: "0.000", wantScale: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := decimal.Parse(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.String(); got != tt.want {
				t.Fatalf("String=%q want=%q", got, tt.want)
			}
			if d.Scale() != tt.wantScale {
				t.Fatalf("Scale=%d want=%d", d.Scale(), tt.wantScale)
			}
			round, err := decimal.Parse(d.String())
			if err != nil || !round.Equal(d) {
				t.Fatalf("roundtrip=%v err=%v", round, err)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{name: "empty", in: "", wantErr: decimal.ErrInvalid},
		{name: "dot", in: ".", wantErr: decimal.ErrInvalid},
		{name: "sign only", in: "-", wantErr: decimal.ErrInvalid},
		{name: "double dot", in: "1.2.3", wantErr: decimal.ErrInvalid},
		{name: "letters", in: "abc", wantErr: decimal.ErrInvalid},
		{name: "space", in: " 1", wantErr: decimal.ErrInvalid},
		{name: "trailing space", in: "1 ", wantErr: decimal.ErrInvalid},
		{name: "nan", in: "NaN", wantErr: decimal.ErrInvalid},
		{name: "empty exponent", in: "1e", wantErr: decimal.ErrInvalid},
		{name: "double exponent", in: "1e2e3", wantErr: decimal.ErrInvalid},
		{name: "exponent overflow", in: "1e10001", wantErr: decimal.ErrScaleOverflow},
		{name: "negative exponent overflow", in: "1e-10001", wantErr: decimal.ErrScaleOverflow},
		{name: "scale overflow", in: "1." + strings.Repeat("1", int(decimal.MaxScale)+1), wantErr: decimal.ErrScaleOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decimal.Parse(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want=%v", err, tt.wantErr)
			}
		})
	}
}

func TestParseScaleNormalization(t *testing.T) {
	s := "1." + strings.Repeat("0", int(decimal.MaxScale)+1)
	d, err := decimal.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Scale() != decimal.MaxScale {
		t.Fatalf("scale=%d", d.Scale())
	}
	if !d.Equal(decimal.NewFromInt64(1)) {
		t.Fatalf("value=%s", d)
	}

	z, err := decimal.Parse("0." + strings.Repeat("0", int(decimal.MaxScale)+5))
	if err != nil {
		t.Fatal(err)
	}
	if z.Scale() != 0 || !z.IsZero() {
		t.Fatalf("zero scale=%d value=%s", z.Scale(), z)
	}
}

func TestAddSub(t *testing.T) {
	a := decimal.MustParse("1.2")
	b := decimal.MustParse("3.45")
	if got := a.Add(b); got.String() != "4.65" {
		t.Fatalf("add=%s", got)
	}
	if got := b.Sub(a); got.String() != "2.25" {
		t.Fatalf("sub=%s", got)
	}
	c := decimal.MustParse("1.20")
	d := decimal.MustParse("0.2")
	if got := c.Sub(d); got.String() != "1.00" {
		t.Fatalf("sub scale=%s", got)
	}
	if got := decimal.MustParse("-1.5").Add(decimal.MustParse("0.5")); got.String() != "-1.0" {
		t.Fatalf("negative add=%s", got)
	}
}

func TestMul(t *testing.T) {
	tests := []struct {
		name  string
		a     string
		b     string
		want  string
		scale int32
	}{
		{name: "simple", a: "1.20", b: "3", want: "3.60", scale: 2},
		{name: "fraction", a: "0.1", b: "0.2", want: "0.02", scale: 2},
		{name: "negative", a: "-2.5", b: "4", want: "-10.0", scale: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decimal.MustParse(tt.a).Mul(decimal.MustParse(tt.b))
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want || got.Scale() != tt.scale {
				t.Fatalf("got=%s scale=%d want=%s scale=%d", got, got.Scale(), tt.want, tt.scale)
			}
		})
	}

	tiny := decimal.MustParse("1e-5000")
	squared, err := tiny.Mul(decimal.MustParse("1"))
	if err != nil || squared.Scale() != 5000 {
		t.Fatalf("scale=%d err=%v", squared.Scale(), err)
	}
	if _, err := squared.Mul(decimal.MustParse("1e-5001")); !errors.Is(err, decimal.ErrScaleOverflow) {
		t.Fatalf("err=%v", err)
	}
}

func TestDiv(t *testing.T) {
	tests := []struct {
		name  string
		a     string
		b     string
		scale int32
		mode  decimal.RoundingMode
		want  string
	}{
		{name: "third half up", a: "10", b: "3", scale: 4, mode: decimal.RoundHalfUp, want: "3.3333"},
		{name: "eighth half up", a: "1", b: "8", scale: 2, mode: decimal.RoundHalfUp, want: "0.13"},
		{name: "eighth half even", a: "1", b: "8", scale: 2, mode: decimal.RoundHalfEven, want: "0.12"},
		{name: "negative floor", a: "-1", b: "8", scale: 2, mode: decimal.RoundFloor, want: "-0.13"},
		{name: "negative ceiling", a: "-1", b: "8", scale: 2, mode: decimal.RoundCeiling, want: "-0.12"},
		{name: "negative down", a: "-1", b: "3", scale: 0, mode: decimal.RoundDown, want: "0"},
		{name: "negative up", a: "-1", b: "3", scale: 0, mode: decimal.RoundUp, want: "-1"},
		{name: "negative floor integer", a: "-1", b: "3", scale: 0, mode: decimal.RoundFloor, want: "-1"},
		{name: "negative ceiling integer", a: "-1", b: "3", scale: 0, mode: decimal.RoundCeiling, want: "0"},
		{name: "scale up", a: "1.5", b: "3", scale: 4, mode: decimal.RoundDown, want: "0.5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decimal.MustParse(tt.a).Div(decimal.MustParse(tt.b), tt.scale, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("got=%s want=%s", got, tt.want)
			}
		})
	}

	if _, err := decimal.MustParse("1").Div(decimal.Decimal{}, 2, decimal.RoundDown); !errors.Is(err, decimal.ErrDivisionByZero) {
		t.Fatalf("err=%v", err)
	}
	if _, err := decimal.MustParse("1").Div(decimal.NewFromInt64(2), -1, decimal.RoundDown); !errors.Is(err, decimal.ErrScaleOverflow) {
		t.Fatalf("err=%v", err)
	}
	if _, err := decimal.MustParse("1").Div(decimal.NewFromInt64(2), 2, decimal.RoundingMode(99)); !errors.Is(err, decimal.ErrInvalidRoundingMode) {
		t.Fatalf("err=%v", err)
	}
}

func TestRound(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		scale int32
		mode  decimal.RoundingMode
		want  string
	}{
		{name: "half up", in: "2.5", scale: 0, mode: decimal.RoundHalfUp, want: "3"},
		{name: "half even down", in: "2.5", scale: 0, mode: decimal.RoundHalfEven, want: "2"},
		{name: "half even up", in: "3.5", scale: 0, mode: decimal.RoundHalfEven, want: "4"},
		{name: "negative half up", in: "-2.5", scale: 0, mode: decimal.RoundHalfUp, want: "-3"},
		{name: "negative half even", in: "-2.5", scale: 0, mode: decimal.RoundHalfEven, want: "-2"},
		{name: "half down", in: "2.5", scale: 0, mode: decimal.RoundHalfDown, want: "2"},
		{name: "fraction half up", in: "1.005", scale: 2, mode: decimal.RoundHalfUp, want: "1.01"},
		{name: "fraction half even", in: "1.005", scale: 2, mode: decimal.RoundHalfEven, want: "1.00"},
		{name: "pad", in: "1.2", scale: 4, mode: decimal.RoundDown, want: "1.2000"},
		{name: "floor negative", in: "-1.29", scale: 1, mode: decimal.RoundFloor, want: "-1.3"},
		{name: "ceiling negative", in: "-1.29", scale: 1, mode: decimal.RoundCeiling, want: "-1.2"},
		{name: "truncate negative", in: "-1.29", scale: 1, mode: decimal.RoundDown, want: "-1.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decimal.MustParse(tt.in).Round(tt.scale, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("got=%s want=%s", got, tt.want)
			}
		})
	}

	d := decimal.MustParse("-1.29")
	if got, err := d.Truncate(1); err != nil || got.String() != "-1.2" {
		t.Fatalf("truncate=%s err=%v", got, err)
	}
	if got, err := d.Floor(1); err != nil || got.String() != "-1.3" {
		t.Fatalf("floor=%s err=%v", got, err)
	}
	if got, err := d.Ceiling(1); err != nil || got.String() != "-1.2" {
		t.Fatalf("ceiling=%s err=%v", got, err)
	}
	if _, err := d.Round(-1, decimal.RoundDown); !errors.Is(err, decimal.ErrScaleOverflow) {
		t.Fatalf("err=%v", err)
	}
}

func TestInt64(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr error
	}{
		{name: "integer", in: "42", want: 42},
		{name: "scaled integer", in: "42.00", want: 42},
		{name: "negative", in: "-42.0", want: -42},
		{name: "fraction", in: "42.01", wantErr: decimal.ErrNotIntegral},
		{name: "overflow", in: "9223372036854775808", wantErr: decimal.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decimal.MustParse(tt.in).Int64()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want=%v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got=%d err=%v want=%d", got, err, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	a := decimal.MustParse("1.0")
	b := decimal.MustParse("1.00")
	if !a.Equal(b) || a.Cmp(b) != 0 {
		t.Fatal("expected equal")
	}
	if !decimal.MustParse("0.99").Less(a) {
		t.Fatal("expected less")
	}
	if !decimal.MustParse("1.01").Greater(a) {
		t.Fatal("expected greater")
	}
	if !a.IsInteger() || decimal.MustParse("1.01").IsInteger() {
		t.Fatal("IsInteger mismatch")
	}
}

func TestSignAndAbs(t *testing.T) {
	d := decimal.MustParse("-1.25")
	if !d.IsNegative() || d.IsPositive() || d.IsZero() {
		t.Fatalf("sign=%d", d.Sign())
	}
	if got := d.Neg().String(); got != "1.25" {
		t.Fatalf("neg=%s", got)
	}
	if got := d.Abs().String(); got != "1.25" {
		t.Fatalf("abs=%s", got)
	}
	var zero decimal.Decimal
	if zero.String() != "0" || zero.Scale() != 0 || !zero.IsZero() {
		t.Fatalf("zero=%q scale=%d", zero.String(), zero.Scale())
	}
}

func TestImmutability(t *testing.T) {
	a := decimal.MustParse("1.20")
	_ = a.Add(decimal.MustParse("2.3"))
	_, _ = a.Mul(decimal.MustParse("2"))
	if got := a.String(); got != "1.20" {
		t.Fatalf("a mutated: %s", got)
	}
	u := a.Unscaled()
	u.SetInt64(999)
	if got := a.String(); got != "1.20" {
		t.Fatalf("unscaled copy mutated receiver: %s", got)
	}
}

func TestJSON(t *testing.T) {
	d := decimal.MustParse("1.20")
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"1.20"` {
		t.Fatalf("json=%s", b)
	}

	var got decimal.Decimal
	if err := json.Unmarshal([]byte(`"1.20"`), &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.20" {
		t.Fatalf("got=%s", got)
	}
	if err := json.Unmarshal([]byte(`1e-2`), &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.01" {
		t.Fatalf("got=%s", got)
	}
	if err := json.Unmarshal([]byte(`null`), &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("got=%s", got)
	}
	if err := json.Unmarshal([]byte(`true`), &got); err == nil {
		t.Fatal("expected error")
	}
}

func TestTextAndSQL(t *testing.T) {
	d := decimal.MustParse("-3.50")
	text, err := d.MarshalText()
	if err != nil || string(text) != "-3.50" {
		t.Fatalf("text=%s err=%v", text, err)
	}
	var got decimal.Decimal
	if err := got.UnmarshalText(text); err != nil || got.String() != "-3.50" {
		t.Fatalf("got=%s err=%v", got, err)
	}

	value, err := d.Value()
	if err != nil || value != "-3.50" {
		t.Fatalf("value=%v err=%v", value, err)
	}
	for _, src := range []any{"1.25", []byte("2.50"), int64(3), 4.5, nil} {
		if err := got.Scan(src); err != nil {
			t.Fatalf("scan %T: %v", src, err)
		}
	}
	if err := got.Scan(struct{}{}); !errors.Is(err, decimal.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := decimal.New(1, -1); !errors.Is(err, decimal.ErrScaleOverflow) {
		t.Fatalf("err=%v", err)
	}
	if _, err := decimal.New(1, decimal.MaxScale+1); !errors.Is(err, decimal.ErrScaleOverflow) {
		t.Fatalf("err=%v", err)
	}
	if d := decimal.NewFromBigInt(nil); !d.IsZero() {
		t.Fatalf("d=%s", d)
	}
	v := big.NewInt(123)
	d := decimal.NewFromBigInt(v)
	v.SetInt64(999)
	if d.String() != "123" {
		t.Fatalf("d=%s", d)
	}
}

func TestFromFloat64(t *testing.T) {
	d, err := decimal.FromFloat64(1.25)
	if err != nil || d.String() != "1.25" {
		t.Fatalf("d=%s err=%v", d, err)
	}
}

package convert_test

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/convert"
)

func TestNumericConversions(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{
			name: "int64 to int32 ok",
			run: func() error {
				got, err := convert.Int64ToInt32(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "int64 to int32 overflow",
			run: func() error {
				_, err := convert.Int64ToInt32(math.MaxInt32 + 1)
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "uint64 to int64 overflow",
			run: func() error {
				_, err := convert.Uint64ToInt64(math.MaxUint64)
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "negative to uint64",
			run: func() error {
				_, err := convert.Int64ToUint64(-1)
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "uint64 to int overflow",
			run: func() error {
				_, err := convert.Uint64ToInt(math.MaxUint64)
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "float truncates",
			run: func() error {
				got, err := convert.Float64ToInt64(-12.9)
				if err != nil || got != -12 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float nan",
			run: func() error {
				_, err := convert.Float64ToInt64(math.NaN())
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "float overflow",
			run: func() error {
				_, err := convert.Float64ToInt64(float64(1 << 63))
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "float min int64 allowed",
			run: func() error {
				got, err := convert.Float64ToInt64(-float64(1 << 63))
				if err != nil || got != math.MinInt64 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float exact rejects fraction",
			run: func() error {
				_, err := convert.Float64ToInt64Exact(1.5)
				return err
			},
			wantErr: convert.ErrInvalid,
		},
		{
			name: "float to int ok",
			run: func() error {
				got, err := convert.Float64ToInt(12.9)
				if err != nil || got != 12 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float32 overflow",
			run: func() error {
				_, err := convert.Float64ToFloat32(math.MaxFloat64)
				return err
			},
			wantErr: convert.ErrOverflow,
		},
		{
			name: "int64 to int ok",
			run: func() error {
				got, err := convert.Int64ToInt(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "uint64 to int64 ok",
			run: func() error {
				got, err := convert.Uint64ToInt64(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "uint64 to int ok",
			run: func() error {
				got, err := convert.Uint64ToInt(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "int64 to uint64 ok",
			run: func() error {
				got, err := convert.Int64ToUint64(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float exact ok",
			run: func() error {
				got, err := convert.Float64ToInt64Exact(42)
				if err != nil || got != 42 {
					t.Fatalf("got=%d err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float32 nan preserved",
			run: func() error {
				got, err := convert.Float64ToFloat32(math.NaN())
				if err != nil || !math.IsNaN(float64(got)) {
					t.Fatalf("got=%v err=%v", got, err)
				}
				return nil
			},
		},
		{
			name: "float32 ok",
			run: func() error {
				got, err := convert.Float64ToFloat32(3.5)
				if err != nil || got != 3.5 {
					t.Fatalf("got=%v err=%v", got, err)
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected err=%v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want=%v", err, tt.wantErr)
			}
		})
	}
}

func TestStringConversions(t *testing.T) {
	if v, err := convert.StringToInt("42"); err != nil || v != 42 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	if v, err := convert.StringToInt64("-9223372036854775808"); err != nil || v != math.MinInt64 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	if v, err := convert.StringToUint64("18446744073709551615"); err != nil || v != math.MaxUint64 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	if v, err := convert.StringToFloat64("3.14"); err != nil || v != 3.14 {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if v, err := convert.StringToBool("TRUE"); err != nil || !v {
		t.Fatalf("v=%v err=%v", v, err)
	}

	invalid := []struct {
		name string
		run  func() error
	}{
		{name: "empty int", run: func() error { _, err := convert.StringToInt(""); return err }},
		{name: "space int", run: func() error { _, err := convert.StringToInt(" 1"); return err }},
		{name: "overflow int64", run: func() error { _, err := convert.StringToInt64("9223372036854775808"); return err }},
		{name: "negative uint", run: func() error { _, err := convert.StringToUint64("-1"); return err }},
		{name: "nan float", run: func() error { _, err := convert.StringToFloat64("NaN"); return err }},
		{name: "inf float", run: func() error { _, err := convert.StringToFloat64("+Inf"); return err }},
		{name: "bad bool", run: func() error { _, err := convert.StringToBool("yes"); return err }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, convert.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "string", in: "x", want: "x"},
		{name: "bytes", in: []byte("ab"), want: "ab"},
		{name: "bool", in: true, want: "true"},
		{name: "int", in: -3, want: "-3"},
		{name: "int8", in: int8(-8), want: "-8"},
		{name: "int16", in: int16(-16), want: "-16"},
		{name: "int32", in: int32(-32), want: "-32"},
		{name: "int64", in: int64(-64), want: "-64"},
		{name: "uint", in: uint(3), want: "3"},
		{name: "uint8", in: uint8(8), want: "8"},
		{name: "uint16", in: uint16(16), want: "16"},
		{name: "uint32", in: uint32(32), want: "32"},
		{name: "uint64", in: uint64(3), want: "3"},
		{name: "float32", in: float32(1.5), want: "1.5"},
		{name: "float64", in: 1.5, want: "1.5"},
		{name: "json number", in: json.Number("1.25"), want: "1.25"},
		{name: "stringer", in: func() *strings.Builder {
			var b strings.Builder
			b.WriteString("built")
			return &b
		}(), want: "built"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convert.ToString(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, tt.want)
			}
		})
	}
	if _, err := convert.ToString(struct{}{}); !errors.Is(err, convert.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	if _, err := convert.ToString(nil); !errors.Is(err, convert.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestToInt64(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    int64
		wantErr error
	}{
		{name: "int", in: int(7), want: 7},
		{name: "int8", in: int8(7), want: 7},
		{name: "int16", in: int16(7), want: 7},
		{name: "int32", in: int32(7), want: 7},
		{name: "int64", in: int64(7), want: 7},
		{name: "uint", in: uint(7), want: 7},
		{name: "uint8", in: uint8(7), want: 7},
		{name: "uint16", in: uint16(7), want: 7},
		{name: "uint32", in: uint32(7), want: 7},
		{name: "uint64", in: uint64(7), want: 7},
		{name: "float32 integral", in: float32(7), want: 7},
		{name: "float integral", in: 7.0, want: 7},
		{name: "float fraction", in: 7.5, wantErr: convert.ErrInvalid},
		{name: "string", in: "7", want: 7},
		{name: "bytes", in: []byte("-7"), want: -7},
		{name: "json int", in: json.Number("7"), want: 7},
		{name: "json float integral", in: json.Number("7.0"), want: 7},
		{name: "uint overflow", in: uint64(math.MaxUint64), wantErr: convert.ErrOverflow},
		{name: "unsupported", in: true, wantErr: convert.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convert.ToInt64(tt.in)
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

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    float64
		wantErr error
	}{
		{name: "int", in: 3, want: 3},
		{name: "int8", in: int8(3), want: 3},
		{name: "int16", in: int16(3), want: 3},
		{name: "int32", in: int32(3), want: 3},
		{name: "int64", in: int64(3), want: 3},
		{name: "uint", in: uint(3), want: 3},
		{name: "uint8", in: uint8(3), want: 3},
		{name: "uint16", in: uint16(3), want: 3},
		{name: "uint32", in: uint32(3), want: 3},
		{name: "uint64", in: uint64(3), want: 3},
		{name: "float32", in: float32(3), want: 3},
		{name: "float64", in: 3.0, want: 3},
		{name: "bytes", in: []byte("3.5"), want: 3.5},
		{name: "string", in: "3.5", want: 3.5},
		{name: "json number", in: json.Number("3.5"), want: 3.5},
		{name: "nan", in: math.NaN(), wantErr: convert.ErrInvalid},
		{name: "inf string", in: "Inf", wantErr: convert.ErrInvalid},
		{name: "unsupported", in: []int{1}, wantErr: convert.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convert.ToFloat64(tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want=%v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got=%v err=%v want=%v", got, err, tt.want)
			}
		})
	}
}

func TestToBool(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    bool
		wantErr error
	}{
		{name: "bool", in: true, want: true},
		{name: "string", in: "false", want: false},
		{name: "bytes", in: []byte("true"), want: true},
		{name: "int 1", in: 1, want: true},
		{name: "int8 0", in: int8(0), want: false},
		{name: "int16 1", in: int16(1), want: true},
		{name: "int32 0", in: int32(0), want: false},
		{name: "int 0", in: int64(0), want: false},
		{name: "uint 1", in: uint(1), want: true},
		{name: "uint8 0", in: uint8(0), want: false},
		{name: "uint16 1", in: uint16(1), want: true},
		{name: "uint32 0", in: uint32(0), want: false},
		{name: "uint64 1", in: uint64(1), want: true},
		{name: "int 2", in: 2, wantErr: convert.ErrInvalid},
		{name: "json", in: json.Number("1"), want: true},
		{name: "unsupported", in: 1.0, wantErr: convert.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convert.ToBool(tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want=%v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got=%v err=%v want=%v", got, err, tt.want)
			}
		})
	}
}

func TestPointerHelpers(t *testing.T) {
	p := convert.Ptr(42)
	if p == nil || *p != 42 {
		t.Fatalf("p=%v", p)
	}
	if got := convert.Value(p); got != 42 {
		t.Fatalf("got=%d", got)
	}
	if got := convert.Value[int](nil); got != 0 {
		t.Fatalf("got=%d", got)
	}
	if got := convert.ValueOr(p, 7); got != 42 {
		t.Fatalf("got=%d", got)
	}
	if got := convert.ValueOr[int](nil, 7); got != 7 {
		t.Fatalf("got=%d", got)
	}
	if got := convert.PtrIf("x", false); got != nil {
		t.Fatalf("got=%v", got)
	}
	if got := convert.PtrIf("x", true); got == nil || *got != "x" {
		t.Fatalf("got=%v", got)
	}
	if got := convert.NilIfZero(0); got != nil {
		t.Fatalf("got=%v", got)
	}
	if got := convert.NilIfZero("x"); got == nil || *got != "x" {
		t.Fatalf("got=%v", got)
	}
	src := 9
	clone := convert.Clone(&src)
	if clone == &src || clone == nil || *clone != 9 {
		t.Fatalf("clone=%v", clone)
	}
	if convert.Clone[int](nil) != nil {
		t.Fatal("nil clone should be nil")
	}
}

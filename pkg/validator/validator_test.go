package validator_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	playground "github.com/go-playground/validator/v10"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/validator"
)

type sampleReq struct {
	Name   string `json:"name" validate:"required,min=2"`
	Amount int64  `json:"amount_cent" validate:"required,gt=0"`
	Curr   string `json:"currency" validate:"required,oneof=CNY USD"`
}

func TestStructRequiredAndMessagesZh(t *testing.T) {
	v := validator.Must(validator.WithLocale(validator.LocaleZhCN))
	err := v.Struct(sampleReq{})
	var ve *validator.ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) == 0 {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(ve.Fields[0].Message, "不能为空") && !strings.Contains(ve.Error(), "不能为空") {
		t.Fatalf("msg=%q", ve.Error())
	}
	if !pkgerrors.Is(err, pkgerrors.ErrInvalidArgument) && pkgerrors.CodeOf(err) != pkgerrors.CodeInvalidArgument {
		t.Fatalf("code=%v", pkgerrors.CodeOf(err))
	}
}

func TestStructOK(t *testing.T) {
	v := validator.Must()
	err := v.Struct(sampleReq{Name: "ab", Amount: 100, Curr: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDecodeJSONUnknownField(t *testing.T) {
	v := validator.Must()
	var req sampleReq
	err := v.DecodeJSON(bytes.NewBufferString(`{"name":"ab","amount_cent":1,"currency":"CNY","extra":1}`), &req)
	if err == nil || !strings.Contains(err.Error(), "unknown") && pkgerrors.CodeOf(err) != pkgerrors.CodeInvalidArgument {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeJSONAndValidate(t *testing.T) {
	v := validator.Must(validator.WithLocale(validator.LocaleEN))
	var req sampleReq
	err := v.DecodeJSON(bytes.NewBufferString(`{"name":"ab","amount_cent":10,"currency":"CNY"}`), &req)
	if err != nil {
		t.Fatal(err)
	}
	err = v.DecodeJSON(bytes.NewBufferString(`{"name":"a","amount_cent":10,"currency":"CNY"}`), &req)
	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err=%v", err)
	}
	found := false
	for _, f := range ve.Fields {
		if f.Field == "name" && f.Tag == "min" {
			found = true
			if !strings.Contains(f.Message, "at least") {
				t.Fatalf("msg=%q", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("fields=%+v", ve.Fields)
	}
}

func TestCustomValidation(t *testing.T) {
	v := validator.Must(validator.WithLocale(validator.LocaleEN))
	err := v.RegisterValidation("odd", func(fl playground.FieldLevel) bool {
		n, ok := fl.Field().Interface().(int)
		return ok && n%2 == 1
	})
	if err != nil {
		t.Fatal(err)
	}
	validator.RegisterBuiltinMessage(validator.LocaleEN, "odd", "{field} must be odd")
	type row struct {
		N int `json:"n" validate:"odd"`
	}
	err = v.Struct(row{N: 2})
	var ve *validator.ValidationError
	if !errors.As(err, &ve) || ve.Fields[0].Tag != "odd" {
		t.Fatalf("err=%v", err)
	}
	if ve.Fields[0].Message != "n must be odd" {
		t.Fatalf("msg=%q", ve.Fields[0].Message)
	}
}

func TestStructContextLocale(t *testing.T) {
	v := validator.Must(validator.WithLocale(validator.LocaleEN))
	err := v.StructContext(validator.LocaleZhCN, sampleReq{})
	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatal(err)
	}
	if !strings.Contains(ve.Error(), "不能为空") {
		t.Fatalf("msg=%q", ve.Error())
	}
	if v.Locale() != validator.LocaleEN {
		t.Fatal("default locale mutated")
	}
}

func TestPublicMessageViaUnwrap(t *testing.T) {
	v := validator.Must(validator.WithLocale(validator.LocaleEN))
	err := v.Struct(sampleReq{Name: "ab", Amount: 0, Curr: "CNY"})
	msg := pkgerrors.PublicMessageOf(err)
	if msg == "" || msg == "internal error" {
		t.Fatalf("public=%q", msg)
	}
}

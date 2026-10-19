package validator

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

// DecodeJSON 将 JSON 解码到 dst 并执行 Struct 校验。
//
// 默认 DisallowUnknownFields；可用 AllowUnknownJSONFields 关闭。
// 解码失败返回 InvalidArgument（无字段细节，避免泄露解析器内部信息）。
func (v *Validator) DecodeJSON(r io.Reader, dst any) error {
	if v == nil {
		return fmt.Errorf("validator: nil")
	}
	if r == nil || dst == nil {
		return pkgerrors.InvalidArgument("invalid json")
	}
	dec := json.NewDecoder(r)
	if v.disallowUnknown {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return mapJSONError(err)
	}
	// 拒绝尾随多余 JSON 值
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return pkgerrors.InvalidArgument("invalid json: trailing data")
		}
		if err != io.EOF {
			return mapJSONError(err)
		}
	}
	return v.Struct(dst)
}

func mapJSONError(err error) error {
	if err == nil {
		return nil
	}
	msg := "invalid json"
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case asJSON(err, &syn):
		msg = "invalid json"
	case asJSON(err, &typ):
		msg = "invalid json: type mismatch"
	case strings.Contains(err.Error(), "unknown field"):
		msg = "invalid json: unknown field"
	}
	return pkgerrors.InvalidArgument(msg)
}

func asJSON(err error, target any) bool {
	return pkgerrors.As(err, target)
}

package validator

import (
	"fmt"
	"strings"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

// FieldError 表示单个字段违反约束。
type FieldError struct {
	// Field 为对外字段名（优先 json tag）。
	Field string `json:"field"`
	// Tag 失败的校验 tag，如 required、min。
	Tag string `json:"tag"`
	// Param tag 参数，如 min=3 中的 3。
	Param string `json:"param,omitempty"`
	// Message 本地化后的可读消息。
	Message string `json:"message"`
}

// Error 实现 error（单字段）。
func (e FieldError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s failed on %s", e.Field, e.Tag)
}

// ValidationError 聚合多字段校验失败；可 errors.As。
type ValidationError struct {
	Fields []FieldError
	cause  error // pkg/errors.InvalidArgument 包装，供 Code/HTTP 映射
}

// Error 实现 error。
func (e *ValidationError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return "validation failed"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Error())
	}
	return strings.Join(parts, "; ")
}

// Unwrap 返回 InvalidArgument 语义错误，供 httpx / apperr 映射 40000。
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// PublicMessage 返回可暴露给客户端的摘要（首条或默认文案）。
func (e *ValidationError) PublicMessage() string {
	if e == nil || len(e.Fields) == 0 {
		return "invalid argument"
	}
	if e.Fields[0].Message != "" {
		return e.Fields[0].Message
	}
	return "invalid argument"
}

func newValidationError(fields []FieldError) *ValidationError {
	msg := "invalid argument"
	if len(fields) > 0 && fields[0].Message != "" {
		msg = fields[0].Message
	}
	return &ValidationError{
		Fields: fields,
		cause:  pkgerrors.InvalidArgument(msg),
	}
}

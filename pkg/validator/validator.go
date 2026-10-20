package validator

import (
	"fmt"
	"sync"

	playground "github.com/go-playground/validator/v10"
)

// Option 配置 Validator。
type Option func(*Validator)

// WithLocale 设置默认文案语言。
func WithLocale(locale Locale) Option {
	return func(v *Validator) {
		if locale != "" {
			v.locale = locale
		}
	}
}

// WithMessageFunc 自定义文案生成。
func WithMessageFunc(fn MessageFunc) Option {
	return func(v *Validator) {
		v.msgFn = fn
	}
}

// WithTagName 结构体校验 tag 名；默认 "validate"。
func WithTagName(name string) Option {
	return func(v *Validator) {
		if name != "" {
			v.validate.SetTagName(name)
		}
	}
}

// AllowUnknownJSONFields 允许 JSON 未知字段（默认拒绝，对齐 SEC）。
func AllowUnknownJSONFields() Option {
	return func(v *Validator) { v.disallowUnknown = false }
}

// Validator 封装 go-playground/validator，带 i18n 与 JSON 解码选项。
type Validator struct {
	validate        *playground.Validate
	locale          Locale
	msgFn           MessageFunc
	disallowUnknown bool
	mu              sync.Mutex
}

// New 创建校验器。
func New(opts ...Option) (*Validator, error) {
	v := &Validator{
		validate:        playground.New(),
		locale:          LocaleZhCN,
		disallowUnknown: true,
	}
	v.validate.RegisterTagNameFunc(fieldName)
	for _, opt := range opts {
		if opt != nil {
			opt(v)
		}
	}
	return v, nil
}

// Must 同 New，失败时 panic。
func Must(opts ...Option) *Validator {
	v, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return v
}

// Engine 返回底层 *validator.Validate，供高级注册（一般用 RegisterValidation）。
func (v *Validator) Engine() *playground.Validate {
	if v == nil {
		return nil
	}
	return v.validate
}

// RegisterValidation 注册自定义 tag。
func (v *Validator) RegisterValidation(tag string, fn playground.Func, callValidationEvenIfNull ...bool) error {
	if v == nil || v.validate == nil {
		return fmt.Errorf("validator: nil")
	}
	if tag == "" || fn == nil {
		return fmt.Errorf("validator: tag and func required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.validate.RegisterValidation(tag, fn, callValidationEvenIfNull...)
}

// Struct 校验结构体；成功返回 nil，失败返回 *ValidationError。
func (v *Validator) Struct(s any) error {
	if v == nil || v.validate == nil {
		return fmt.Errorf("validator: nil")
	}
	if s == nil {
		return newValidationError([]FieldError{{
			Field:   "_",
			Tag:     "required",
			Message: formatMessage(v.locale, "_", "required", "", v.msgFn),
		}})
	}
	err := v.validate.Struct(s)
	if err == nil {
		return nil
	}
	return v.translate(err)
}

// Var 校验单值，tag 如 "required,min=1"。
func (v *Validator) Var(field any, tag string) error {
	if v == nil || v.validate == nil {
		return fmt.Errorf("validator: nil")
	}
	err := v.validate.Var(field, tag)
	if err == nil {
		return nil
	}
	return v.translate(err)
}

// Locale 返回当前默认语言。
func (v *Validator) Locale() Locale {
	if v == nil || v.locale == "" {
		return LocaleZhCN
	}
	return v.locale
}

// StructContext 使用指定语言校验（不修改默认 Locale，并发安全）。
func (v *Validator) StructContext(locale Locale, s any) error {
	if v == nil || v.validate == nil {
		return fmt.Errorf("validator: nil")
	}
	if locale == "" {
		locale = v.Locale()
	}
	if s == nil {
		return newValidationError([]FieldError{{
			Field:   "_",
			Tag:     "required",
			Message: formatMessage(locale, "_", "required", "", v.msgFn),
		}})
	}
	err := v.validate.Struct(s)
	if err == nil {
		return nil
	}
	return v.translateLocale(locale, err)
}

func (v *Validator) translate(err error) error {
	return v.translateLocale(v.Locale(), err)
}

func (v *Validator) translateLocale(locale Locale, err error) error {
	var verrs playground.ValidationErrors
	if !asValidationErrors(err, &verrs) {
		return newValidationError([]FieldError{{
			Field:   "_",
			Tag:     "default",
			Message: formatMessage(locale, "_", "default", "", v.msgFn),
		}})
	}
	fields := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		name := fe.Field()
		if name == "" {
			name = namespacePath(fe.Namespace())
		}
		fields = append(fields, FieldError{
			Field:   name,
			Tag:     fe.Tag(),
			Param:   fe.Param(),
			Message: formatMessage(locale, name, fe.Tag(), fe.Param(), v.msgFn),
		})
	}
	return newValidationError(fields)
}

func asValidationErrors(err error, target *playground.ValidationErrors) bool {
	if err == nil || target == nil {
		return false
	}
	if e, ok := err.(playground.ValidationErrors); ok {
		*target = e
		return true
	}
	return false
}

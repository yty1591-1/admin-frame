package sms

import (
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const (
	maxSignNameRunes    = 32
	maxTemplateIDBytes  = 64
	maxParams           = 20
	maxParamKeyBytes    = 32
	maxParamValueBytes  = 100
	maxParamsBytes      = 1024
	maxContentRunes     = 500
	maxIdempotencyBytes = 128
	maxReferenceBytes   = 128
	phonePattern        = `^\+[1-9]\d{2,14}$`
	localePattern       = `^[a-z]{2}(-[A-Z]{2})?$`
	paramKeyPattern     = `^[A-Za-z0-9_]+$`
)

var (
	phoneRE    = regexp.MustCompile(phonePattern)
	localeRE   = regexp.MustCompile(localePattern)
	paramKeyRE = regexp.MustCompile(paramKeyPattern)
)

// Message 是一条已渲染的短信；模板与裸内容二选一。
//
// 零值非法，Validate 返回 ErrInvalidMessage。Message 可安全复制，
// 但 Params 是 map，调用方不得在 Send 期间并发修改。
type Message struct {
	// Phone 为 E.164 格式（如 +8613800138000）；驱动负责转换为厂商格式。
	Phone string
	// SignName 为已报备的签名；国内驱动必填，未设置时使用 Config 默认签名。
	SignName string
	// TemplateID 为厂商模板编码；与 Content 互斥。
	TemplateID string
	// Params 为模板变量；键名/取值由模板定义，禁止 CR/LF 与控制字符。
	Params map[string]string
	// Content 为裸内容，仅当 Config.AllowRawContent=true 时允许。
	Content string
	// Locale 为语言/地区提示（如 zh-CN），用于国际短信路由。
	Locale string
	// IdempotencyKey 由调用方提供，用于编排层去重与排障关联。
	IdempotencyKey string
	// Reference 为业务引用（如 order_id），只允许低基数、非 PII。
	Reference string
}

// Validate 校验消息结构；不访问网络、不读取配置。
//
// 返回 ErrInvalidRecipient（手机号非法）或 ErrInvalidMessage（其余结构问题），
// 两者都包装 pkg/errors 语义错误。裸内容是否启用由驱动在 Send 时按配置判定。
func (m Message) Validate() error {
	if !phoneRE.MatchString(m.Phone) {
		return invalidRecipient("phone must be E.164 (e.g. +8613800138000)")
	}
	if m.SignName != "" {
		if utf8.RuneCountInString(m.SignName) > maxSignNameRunes {
			return invalidMessage("sign name exceeds %d runes", maxSignNameRunes)
		}
		if containsControl(m.SignName) {
			return invalidMessage("sign name contains control characters")
		}
	}
	hasTemplate := m.TemplateID != ""
	hasContent := m.Content != ""
	switch {
	case hasTemplate && hasContent:
		return invalidMessage("template id and content are mutually exclusive")
	case !hasTemplate && !hasContent:
		return invalidMessage("template id or content is required")
	}
	if hasTemplate {
		if len(m.TemplateID) > maxTemplateIDBytes || containsControl(m.TemplateID) {
			return invalidMessage("template id is invalid")
		}
		if err := validateParams(m.Params); err != nil {
			return err
		}
	} else if len(m.Params) > 0 {
		return invalidMessage("params are only allowed with a template id")
	}
	if hasContent {
		if utf8.RuneCountInString(m.Content) > maxContentRunes {
			return invalidMessage("content exceeds %d runes", maxContentRunes)
		}
		if containsControl(m.Content) {
			return invalidMessage("content contains control characters")
		}
	}
	if m.Locale != "" && !localeRE.MatchString(m.Locale) {
		return invalidMessage("locale must match %s", localePattern)
	}
	if len(m.IdempotencyKey) > maxIdempotencyBytes {
		return invalidMessage("idempotency key exceeds %d bytes", maxIdempotencyBytes)
	}
	if containsControl(m.IdempotencyKey) {
		return invalidMessage("idempotency key contains control characters")
	}
	if len(m.Reference) > maxReferenceBytes {
		return invalidMessage("reference exceeds %d bytes", maxReferenceBytes)
	}
	if containsControl(m.Reference) {
		return invalidMessage("reference contains control characters")
	}
	return nil
}

func validateParams(params map[string]string) error {
	if len(params) > maxParams {
		return invalidMessage("params exceed %d entries", maxParams)
	}
	total := 0
	for key, value := range params {
		if key == "" || len(key) > maxParamKeyBytes || !paramKeyRE.MatchString(key) {
			return invalidMessage("param key %q must be 1-%d bytes of [A-Za-z0-9_]", key, maxParamKeyBytes)
		}
		if len(value) > maxParamValueBytes {
			return invalidMessage("param %q exceeds %d bytes", key, maxParamValueBytes)
		}
		if containsControl(value) {
			return invalidMessage("param %q contains control characters", key)
		}
		total += len(key) + len(value)
	}
	if total > maxParamsBytes {
		return invalidMessage("params exceed %d bytes in total", maxParamsBytes)
	}
	return nil
}

// String 返回脱敏摘要，不包含手机号、模板参数与正文。
func (m Message) String() string {
	kind := "content"
	if m.TemplateID != "" {
		kind = "template=" + m.TemplateID
	}
	return fmt.Sprintf("sms.Message{%s, locale=%q, reference=%q}", kind, m.Locale, m.Reference)
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func cloneParams(params map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}

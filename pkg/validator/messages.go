package validator

import (
	"strings"
)

// Locale 消息语言标签，如 en、zh-CN。
type Locale string

const (
	// LocaleEN 英文。
	LocaleEN Locale = "en"
	// LocaleZhCN 简体中文。
	LocaleZhCN Locale = "zh-CN"
)

// MessageFunc 根据 tag/field/param 生成文案；返回空串时回落内置模板。
type MessageFunc func(locale Locale, field, tag, param string) string

var builtinMessages = map[Locale]map[string]string{
	LocaleEN: {
		"required": "{field} is required",
		"min":      "{field} must be at least {param}",
		"max":      "{field} must be at most {param}",
		"len":      "{field} must be length {param}",
		"eq":       "{field} must equal {param}",
		"ne":       "{field} must not equal {param}",
		"gt":       "{field} must be greater than {param}",
		"gte":      "{field} must be greater than or equal to {param}",
		"lt":       "{field} must be less than {param}",
		"lte":      "{field} must be less than or equal to {param}",
		"oneof":    "{field} must be one of [{param}]",
		"email":    "{field} must be a valid email",
		"uuid":     "{field} must be a valid UUID",
		"url":      "{field} must be a valid URL",
		"numeric":  "{field} must be numeric",
		"alpha":    "{field} must contain only letters",
		"alphanum": "{field} must be alphanumeric",
		"datetime": "{field} must be a valid datetime",
		"default":  "{field} is invalid",
	},
	LocaleZhCN: {
		"required": "{field} 不能为空",
		"min":      "{field} 不能小于 {param}",
		"max":      "{field} 不能大于 {param}",
		"len":      "{field} 长度必须为 {param}",
		"eq":       "{field} 必须等于 {param}",
		"ne":       "{field} 不能等于 {param}",
		"gt":       "{field} 必须大于 {param}",
		"gte":      "{field} 必须大于等于 {param}",
		"lt":       "{field} 必须小于 {param}",
		"lte":      "{field} 必须小于等于 {param}",
		"oneof":    "{field} 必须是 [{param}] 之一",
		"email":    "{field} 必须是合法邮箱",
		"uuid":     "{field} 必须是合法 UUID",
		"url":      "{field} 必须是合法 URL",
		"numeric":  "{field} 必须是数字",
		"alpha":    "{field} 只能包含字母",
		"alphanum": "{field} 只能包含字母和数字",
		"datetime": "{field} 必须是合法时间",
		"default":  "{field} 不合法",
	},
}

func formatMessage(locale Locale, field, tag, param string, custom MessageFunc) string {
	if custom != nil {
		if m := custom(locale, field, tag, param); m != "" {
			return m
		}
	}
	table, ok := builtinMessages[locale]
	if !ok {
		table = builtinMessages[LocaleEN]
	}
	tpl, ok := table[tag]
	if !ok {
		tpl = table["default"]
	}
	out := strings.ReplaceAll(tpl, "{field}", field)
	out = strings.ReplaceAll(out, "{param}", param)
	out = strings.ReplaceAll(out, "{tag}", tag)
	return out
}

// RegisterBuiltinMessage 覆盖或追加内置模板（进程级；测试可用）。
func RegisterBuiltinMessage(locale Locale, tag, template string) {
	if builtinMessages[locale] == nil {
		builtinMessages[locale] = make(map[string]string)
	}
	builtinMessages[locale][tag] = template
}

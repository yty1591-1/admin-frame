package scim

// 过滤表达式解析：只支持 `<attr> eq <value>` 与 `and` 连接（docs/identity-authn.md §21.1.4）。
//
// 不实现通用表达式树：SCIM 客户端常用的过滤面很窄，收敛解析面可以
// 彻底排除把过滤器直接拼进 SQL 的注入风险。

import (
	"regexp"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// filterTermPattern 匹配单个 `attr op value` 项。
var filterTermPattern = regexp.MustCompile(`^([A-Za-z]+)\s+([A-Za-z]+)\s+(.+)$`)

// userFilter 解析后的 User 过滤条件。
type userFilter struct {
	UserName    string
	ExternalID  string
	DisplayName string
	ID          string
	Active      *bool
}

// groupFilter 解析后的 Group 过滤条件。
type groupFilter struct {
	DisplayName string
	ExternalID  string
	ID          string
}

// parseUserFilter 解析 User 过滤器；空串表示不过滤。
func parseUserFilter(raw string) (userFilter, error) {
	var out userFilter
	terms, err := splitFilter(raw)
	if err != nil {
		return out, err
	}
	for _, term := range terms {
		attr, value, termErr := parseFilterTerm(term)
		if termErr != nil {
			return out, termErr
		}
		switch attr {
		case "username":
			out.UserName = value
		case "externalid":
			out.ExternalID = value
		case "displayname":
			out.DisplayName = value
		case "id":
			out.ID = value
		case "active":
			active, boolErr := parseBool(value)
			if boolErr != nil {
				return out, boolErr
			}
			out.Active = &active
		default:
			return out, &app.ScimError{
				Status: 400, ScimType: "invalidFilter",
				Detail: "unsupported filter attribute: " + attr,
			}
		}
	}
	return out, nil
}

// parseGroupFilter 解析 Group 过滤器；空串表示不过滤。
func parseGroupFilter(raw string) (groupFilter, error) {
	var out groupFilter
	terms, err := splitFilter(raw)
	if err != nil {
		return out, err
	}
	for _, term := range terms {
		attr, value, termErr := parseFilterTerm(term)
		if termErr != nil {
			return out, termErr
		}
		switch attr {
		case "displayname":
			out.DisplayName = value
		case "externalid":
			out.ExternalID = value
		case "id":
			out.ID = value
		default:
			return out, &app.ScimError{
				Status: 400, ScimType: "invalidFilter",
				Detail: "unsupported filter attribute: " + attr,
			}
		}
	}
	return out, nil
}

// filterAndPattern 匹配过滤项之间的 and 连接符（大小写不敏感）。
var filterAndPattern = regexp.MustCompile(`(?i)\s+and\s+`)

// splitFilter 按 and 拆分过滤项；空串返回 nil。
//
// 引号内的 " and " 不参与拆分：先按字符扫描并维护引号状态，
// 只在引号外、且当前位置正好是连接符起点时切分。
func splitFilter(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parts := make([]string, 0, 2)
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(trimmed); {
		char := trimmed[i]
		if char == '"' {
			inQuote = !inQuote
			current.WriteByte(char)
			i++
			continue
		}
		if !inQuote && (char == ' ' || char == '\t') {
			if loc := filterAndPattern.FindStringIndex(trimmed[i:]); loc != nil && loc[0] == 0 {
				parts = append(parts, strings.TrimSpace(current.String()))
				current.Reset()
				i += loc[1]
				continue
			}
		}
		current.WriteByte(char)
		i++
	}
	if inQuote {
		return nil, &app.ScimError{Status: 400, ScimType: "invalidFilter", Detail: "unterminated quoted value"}
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	for _, part := range parts {
		if part == "" {
			return nil, &app.ScimError{Status: 400, ScimType: "invalidFilter", Detail: "empty filter term"}
		}
	}
	return parts, nil
}

// parseFilterTerm 解析单个 `<attr> eq <value>`；非 eq 或未知属性都拒绝。
func parseFilterTerm(term string) (attr, value string, err error) {
	matches := filterTermPattern.FindStringSubmatch(strings.TrimSpace(term))
	if matches == nil {
		return "", "", &app.ScimError{
			Status: 400, ScimType: "invalidFilter", Detail: "filter must be <attr> eq <value>",
		}
	}
	op := strings.ToLower(matches[2])
	if op != "eq" {
		return "", "", &app.ScimError{
			Status: 400, ScimType: "invalidFilter", Detail: "only eq is supported",
		}
	}
	raw := strings.TrimSpace(matches[3])
	if strings.HasPrefix(raw, `"`) {
		if len(raw) < 2 || !strings.HasSuffix(raw, `"`) {
			return "", "", &app.ScimError{
				Status: 400, ScimType: "invalidFilter", Detail: "quoted filter value is not terminated",
			}
		}
		raw = strings.ReplaceAll(raw[1:len(raw)-1], `\"`, `"`)
	}
	return strings.ToLower(matches[1]), raw, nil
}

// parseBool 解析布尔字面量；SCIM 只接受 true / false。
func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, &app.ScimError{
			Status: 400, ScimType: "invalidFilter", Detail: "boolean filter value must be true or false",
		}
	}
}

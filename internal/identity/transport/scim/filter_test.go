package scim

import (
	"strings"
	"testing"
)

// TestParseUserFilter 覆盖支持的属性与拒绝的语法。
func TestParseUserFilter(t *testing.T) {
	filter, err := parseUserFilter(`userName eq "Alice@Corp.example" and active eq true`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if filter.UserName != "Alice@Corp.example" || filter.Active == nil || !*filter.Active {
		t.Fatalf("filter = %+v, want userName + active=true", filter)
	}

	// 值里包含 " and " 时不应被当成连接符。
	filter, err = parseUserFilter(`displayName eq "Ops and Sec"`)
	if err != nil {
		t.Fatalf("parse quoted and: %v", err)
	}
	if filter.DisplayName != "Ops and Sec" {
		t.Fatalf("displayName = %q", filter.DisplayName)
	}

	cases := []struct {
		name   string
		raw    string
		detail string
	}{
		{name: "unsupported op", raw: `userName co "a"`, detail: "only eq is supported"},
		{name: "unknown attr", raw: `emails eq "a"`, detail: "unsupported filter attribute"},
		{name: "bad boolean", raw: `active eq yes`, detail: "boolean filter value"},
		{name: "unterminated", raw: `userName eq "a`, detail: "unterminated"},
		// 末尾只有 and：去掉首尾空白后连接符不成立，按未闭合引号拒绝（仍是 400 invalidFilter）。
		{name: "trailing and", raw: `userName eq "a" and `, detail: "not terminated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseUserFilter(tc.raw)
			if err == nil {
				t.Fatalf("want error")
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("err = %v, want detail %q", err, tc.detail)
			}
		})
	}
}

// TestParseGroupFilter 覆盖组过滤属性白名单。
func TestParseGroupFilter(t *testing.T) {
	filter, err := parseGroupFilter(`displayName eq "ops" and externalId eq "g-1"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if filter.DisplayName != "ops" || filter.ExternalID != "g-1" {
		t.Fatalf("filter = %+v", filter)
	}
	if _, err := parseGroupFilter(`members eq "x"`); err == nil {
		t.Fatalf("members filter must be rejected")
	}
}

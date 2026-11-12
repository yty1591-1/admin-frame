package domain

import (
	"strings"
	"testing"
)

func validDurationSpec() KeySpec {
	return KeySpec{
		Namespace:   "platform",
		Key:         "http.timeout",
		ValueType:   ValueTypeDuration,
		Default:     "10s",
		Description: "HTTP 请求超时",
		Owner:       "team-platform",
		Reloadable:  true,
	}
}

func TestKeySpecValidate(t *testing.T) {
	if err := validDurationSpec().Validate(); err != nil {
		t.Fatalf("valid key spec rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*KeySpec)
	}{
		{"invalid namespace", func(s *KeySpec) { s.Namespace = "-bad" }},
		{"invalid key", func(s *KeySpec) { s.Key = "HTTP" }},
		{"invalid type", func(s *KeySpec) { s.ValueType = "float" }},
		{"missing description", func(s *KeySpec) { s.Description = "" }},
		{"missing owner", func(s *KeySpec) { s.Owner = " " }},
		{"sensitive with default", func(s *KeySpec) { s.Sensitive = true }},
		{"required with default", func(s *KeySpec) { s.Required = true }},
		{"invalid default", func(s *KeySpec) { s.Default = "ten seconds" }},
		{"enum on non string", func(s *KeySpec) { s.Enum = []string{"a"} }},
		{"min on non int", func(s *KeySpec) {
			minValue := int64(1)
			s.MinInt = &minValue
		}},
		{"pattern on non string", func(s *KeySpec) { s.Pattern = "^a$" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keySpec := validDurationSpec()
			tc.mutate(&keySpec)
			if err := keySpec.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestKeySpecValidateEnumAndRange(t *testing.T) {
	enumSpec := KeySpec{
		Namespace: "platform", Key: "log.level", ValueType: ValueTypeString,
		Default: "info", Description: "日志级别", Owner: "team-platform",
		Enum: []string{"debug", "info", "warn"},
	}
	if err := enumSpec.Validate(); err != nil {
		t.Fatalf("valid enum spec rejected: %v", err)
	}

	badDefault := enumSpec
	badDefault.Default = "trace"
	if err := badDefault.Validate(); err == nil {
		t.Fatal("default outside enum must be rejected")
	}

	dupEnum := enumSpec
	dupEnum.Enum = []string{"info", "info"}
	if err := dupEnum.Validate(); err == nil {
		t.Fatal("duplicate enum value must be rejected")
	}

	minValue, maxValue := int64(1), int64(10)
	intSpec := KeySpec{
		Namespace: "platform", Key: "http.retries", ValueType: ValueTypeInt,
		Default: "3", Description: "重试次数", Owner: "team-platform",
		MinInt: &minValue, MaxInt: &maxValue,
	}
	if err := intSpec.Validate(); err != nil {
		t.Fatalf("valid int spec rejected: %v", err)
	}

	inverted := intSpec
	low, high := int64(10), int64(1)
	inverted.MinInt, inverted.MaxInt = &low, &high
	if err := inverted.Validate(); err == nil {
		t.Fatal("min > max must be rejected")
	}

	outOfRange := intSpec
	outOfRange.Default = "99"
	if err := outOfRange.Validate(); err == nil {
		t.Fatal("default outside range must be rejected")
	}

	patternSpec := KeySpec{
		Namespace: "platform", Key: "http.host", ValueType: ValueTypeString,
		Default: "api.example.com", Description: "上游主机", Owner: "team-platform",
		Pattern: `^[a-z0-9.]+$`,
	}
	if err := patternSpec.Validate(); err != nil {
		t.Fatalf("valid pattern spec rejected: %v", err)
	}
	badPattern := patternSpec
	badPattern.Pattern = "(["
	if err := badPattern.Validate(); err == nil {
		t.Fatal("invalid pattern must be rejected")
	}
	nonMatching := patternSpec
	nonMatching.Default = "API.EXAMPLE.COM"
	if err := nonMatching.Validate(); err == nil {
		t.Fatal("default not matching pattern must be rejected")
	}
}

func TestKeySpecValidateValue(t *testing.T) {
	minValue, maxValue := int64(1), int64(10)
	intSpec := KeySpec{Namespace: "platform", Key: "http.retries", ValueType: ValueTypeInt,
		Description: "重试次数", Owner: "team", MinInt: &minValue, MaxInt: &maxValue}
	enumSpec := KeySpec{Namespace: "platform", Key: "log.level", ValueType: ValueTypeString,
		Description: "日志级别", Owner: "team", Enum: []string{"info", "warn"}}
	patternSpec := KeySpec{Namespace: "platform", Key: "http.host", ValueType: ValueTypeString,
		Description: "主机", Owner: "team", Pattern: `^[a-z.]+$`}
	boolSpec := KeySpec{Namespace: "platform", Key: "feature.on", ValueType: ValueTypeBool,
		Description: "开关", Owner: "team"}
	jsonSpec := KeySpec{Namespace: "platform", Key: "limits.json", ValueType: ValueTypeJSON,
		Description: "限制", Owner: "team"}

	if err := intSpec.ValidateValue(""); err != nil {
		t.Fatalf("empty value must be allowed: %v", err)
	}
	if err := intSpec.ValidateValue("5"); err != nil {
		t.Fatalf("valid int rejected: %v", err)
	}
	if err := intSpec.ValidateValue("x"); err == nil {
		t.Fatal("non-integer must be rejected")
	}
	if err := intSpec.ValidateValue("0"); err == nil {
		t.Fatal("below-min must be rejected")
	}
	if err := intSpec.ValidateValue("11"); err == nil {
		t.Fatal("above-max must be rejected")
	}

	if err := enumSpec.ValidateValue("warn"); err != nil {
		t.Fatalf("enum member rejected: %v", err)
	}
	if err := enumSpec.ValidateValue("debug"); err == nil {
		t.Fatal("non-member must be rejected")
	}

	if err := patternSpec.ValidateValue("api.example"); err != nil {
		t.Fatalf("pattern match rejected: %v", err)
	}
	if err := patternSpec.ValidateValue("API.example"); err == nil {
		t.Fatal("pattern mismatch must be rejected")
	}

	if err := boolSpec.ValidateValue("true"); err != nil {
		t.Fatalf("valid bool rejected: %v", err)
	}
	if err := boolSpec.ValidateValue("yes"); err == nil {
		t.Fatal("invalid bool must be rejected")
	}

	if err := jsonSpec.ValidateValue(`{"a":1}`); err != nil {
		t.Fatalf("valid json rejected: %v", err)
	}
	if err := jsonSpec.ValidateValue(`{`); err == nil {
		t.Fatal("invalid json must be rejected")
	}

	sensitive := KeySpec{Namespace: "platform", Key: "smtp.password", ValueType: ValueTypeString,
		Description: "SMTP 口令", Owner: "team", Sensitive: true}
	if err := sensitive.ValidateValue("secret"); err == nil {
		t.Fatal("sensitive value must be rejected in M1")
	}
}

func TestKeySpecValidationJSON(t *testing.T) {
	spec := KeySpec{Namespace: "platform", Key: "http.retries", ValueType: ValueTypeInt,
		Description: "重试次数", Owner: "team"}
	if got := spec.ValidationJSON(); got != "" {
		t.Fatalf("spec without rules must produce empty JSON, got %q", got)
	}

	minValue, maxValue := int64(1), int64(10)
	spec.MinInt, spec.MaxInt = &minValue, &maxValue
	raw := spec.ValidationJSON()
	if !strings.Contains(raw, `"min":1`) || !strings.Contains(raw, `"max":10`) {
		t.Fatalf("validation json missing range: %s", raw)
	}

	enumSpec := KeySpec{Namespace: "platform", Key: "log.level", ValueType: ValueTypeString,
		Description: "日志级别", Owner: "team", Enum: []string{"info", "warn"}, Pattern: `^[a-z]+$`}
	raw = enumSpec.ValidationJSON()
	if !strings.Contains(raw, `"enum":["info","warn"]`) || !strings.Contains(raw, `"pattern"`) {
		t.Fatalf("validation json missing enum/pattern: %s", raw)
	}
}

package configboot

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// requiredValues 从 key spec 生成一份「所有必填键都有值」的 Values。
func requiredValues(t *testing.T) map[string]string {
	t.Helper()
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("load key spec: %v", err)
	}
	values := make(map[string]string)
	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			if keySpec.Required && !keySpec.Sensitive {
				values[keySpec.Key] = "test-" + keySpec.Key
			}
		}
	}
	return values
}

func TestMissingRequiredEmptyValues(t *testing.T) {
	gaps, err := MissingRequired(map[string]string{})
	if err != nil {
		t.Fatalf("MissingRequired: %v", err)
	}
	if len(gaps) == 0 {
		t.Fatal("empty values must report blocking keys")
	}
	for _, gap := range gaps {
		if !strings.Contains(gap, "/") {
			t.Fatalf("gap %q must be namespace/key", gap)
		}
	}
	if gaps[0] != "platform/auth.jwt.audience" {
		t.Fatalf("gaps must be sorted, got first %q", gaps[0])
	}
}

func TestMissingRequiredSatisfiedValues(t *testing.T) {
	gaps, err := MissingRequired(requiredValues(t))
	if err != nil {
		t.Fatalf("MissingRequired: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("fully seeded values must not block, got %v", gaps)
	}
}

func TestMissingRequiredIgnoresSensitiveAndBlank(t *testing.T) {
	values := requiredValues(t)
	values["auth.jwt.issuer"] = "   "
	gaps, err := MissingRequired(values)
	if err != nil {
		t.Fatalf("MissingRequired: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != "platform/auth.jwt.issuer" {
		t.Fatalf("blank value must be reported alone, got %v", gaps)
	}
}

func TestRequireComplete(t *testing.T) {
	if err := RequireComplete("biz-api", requiredValues(t)); err != nil {
		t.Fatalf("complete values must pass, got %v", err)
	}
	err := RequireComplete("biz-api", map[string]string{})
	if err == nil {
		t.Fatal("incomplete values must fail")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "biz-api: config incomplete") {
		t.Fatalf("error must name the process, got %q", msg)
	}
	if !strings.Contains(msg, "run the admin install wizard") {
		t.Fatalf("error must tell operators how to fix it, got %q", msg)
	}
}

// TestMissingRequiredStorageConditional 守护对象存储的条件必填：
// 只有 s3/minio 才要求 bucket/region，别的驱动不该被判缺键。
func TestMissingRequiredStorageConditional(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		bucket string
		region string
		want   []string
	}{
		{
			name: "s3 缺 bucket 与 region", driver: "s3",
			want: []string{"platform/storage.s3.bucket", "platform/storage.s3.region"},
		},
		{
			name: "minio 缺 region", driver: "minio", bucket: "b",
			want: []string{"platform/storage.s3.region"},
		},
		{
			name: "s3 已全填", driver: "s3", bucket: "b", region: "r",
			want: nil,
		},
		{
			name: "memory 不需要桶参数", driver: "memory",
			want: nil,
		},
		{
			name: "local 不需要桶参数", driver: "local",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := requiredValues(t)
			values["storage.driver"] = tc.driver
			values["storage.s3.bucket"] = tc.bucket
			values["storage.s3.region"] = tc.region
			gaps, err := MissingRequired(values)
			if err != nil {
				t.Fatalf("MissingRequired: %v", err)
			}
			var got []string
			for _, gap := range gaps {
				if strings.HasPrefix(gap, "platform/storage.") {
					got = append(got, gap)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("storage gaps = %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Fatalf("storage gaps = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

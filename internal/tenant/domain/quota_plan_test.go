package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// TestNewQuota 构造期校验：资源键字符集、长度、上限下界与「不限」语义。
func TestNewQuota(t *testing.T) {
	quota, err := domain.NewQuota(" t-1 ", " api.rps ", 100, fixedNow)
	if err != nil {
		t.Fatalf("NewQuota() error = %v", err)
	}
	if quota.TenantID != "t-1" || quota.Resource != "api.rps" || quota.Limit != 100 {
		t.Fatalf("NewQuota() must trim and keep values: %+v", quota)
	}
	if !quota.UpdatedAt.Equal(fixedNow) {
		t.Fatalf("NewQuota() UpdatedAt = %v", quota.UpdatedAt)
	}

	unlimited, err := domain.NewQuota("t-1", "user", domain.QuotaUnlimited, fixedNow)
	if err != nil {
		t.Fatalf("NewQuota(unlimited) error = %v", err)
	}
	if !unlimited.Unlimited() {
		t.Fatal("limit -1 must mean unlimited")
	}

	cases := []struct {
		name     string
		tenant   string
		resource string
		limit    int64
		wantSent error
	}{
		{"empty tenant", " ", "user", 1, nil},
		{"empty resource", "t-1", " ", 1, domain.ErrResourceRequired},
		{"resource too long", "t-1", strings.Repeat("a", domain.MaxResourceLength+1), 1, domain.ErrResourceTooLong},
		{"invalid resource chars", "t-1", "api rps", 1, nil},
		{"uppercase resource", "t-1", "API.RPS", 1, nil},
		{"limit below -1", "t-1", "user", -2, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewQuota(tc.tenant, tc.resource, tc.limit, fixedNow)
			if err == nil {
				t.Fatal("NewQuota() must fail")
			}
			if tc.wantSent != nil && !errors.Is(err, tc.wantSent) {
				t.Fatalf("NewQuota() error = %v, want %v", err, tc.wantSent)
			}
			if tc.name == "invalid resource chars" || tc.name == "uppercase resource" {
				var resErr *domain.InvalidResourceError
				if !errors.As(err, &resErr) {
					t.Fatalf("NewQuota() error = %v, want InvalidResourceError", err)
				}
			}
		})
	}
}

// TestQuotaValidate 回读校验与 Unlimited 语义。
func TestQuotaValidate(t *testing.T) {
	valid := domain.Quota{TenantID: "t-1", Resource: "storage.mb", Limit: 1024}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if valid.Unlimited() {
		t.Fatal("positive limit must not be unlimited")
	}

	cases := []struct {
		name   string
		mutate func(domain.Quota) domain.Quota
	}{
		{"empty tenant", func(q domain.Quota) domain.Quota { q.TenantID = " "; return q }},
		{"empty resource", func(q domain.Quota) domain.Quota { q.Resource = ""; return q }},
		{"invalid resource", func(q domain.Quota) domain.Quota { q.Resource = "api/rps"; return q }},
		{"limit below -1", func(q domain.Quota) domain.Quota { q.Limit = -2; return q }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(valid).Validate(); err == nil {
				t.Fatal("Validate() must fail")
			}
		})
	}
}

// TestPlanValidate 套餐模板校验：编码字符集、展示名、模板配额（无租户维度）。
func TestPlanValidate(t *testing.T) {
	valid := domain.Plan{
		ID:   "plan-1",
		Code: "standard",
		Name: "标准版",
		Quotas: []domain.Quota{
			{Resource: "user", Limit: 10}, // 模板配额没有 TenantID
			{Resource: "api.rps", Limit: domain.QuotaUnlimited},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cases := []struct {
		name     string
		mutate   func(domain.Plan) domain.Plan
		wantSent error
	}{
		{"missing id", func(p domain.Plan) domain.Plan { p.ID = " "; return p }, nil},
		{"missing code", func(p domain.Plan) domain.Plan { p.Code = ""; return p }, domain.ErrPlanCodeRequired},
		{"code too long", func(p domain.Plan) domain.Plan {
			p.Code = strings.Repeat("a", domain.MaxPlanCodeLength+1)
			return p
		}, domain.ErrPlanCodeTooLong},
		{"invalid code", func(p domain.Plan) domain.Plan { p.Code = "Standard Plan"; return p }, nil},
		{"missing name", func(p domain.Plan) domain.Plan { p.Name = ""; return p }, nil},
		{"quota empty resource", func(p domain.Plan) domain.Plan {
			p.Quotas = []domain.Quota{{Resource: " ", Limit: 1}}
			return p
		}, domain.ErrResourceRequired},
		{"quota invalid resource", func(p domain.Plan) domain.Plan {
			p.Quotas = []domain.Quota{{Resource: "API.RPS", Limit: 1}}
			return p
		}, nil},
		{"quota limit below -1", func(p domain.Plan) domain.Plan {
			p.Quotas = []domain.Quota{{Resource: "user", Limit: -5}}
			return p
		}, nil},
		{"duplicate quota resource", func(p domain.Plan) domain.Plan {
			p.Quotas = []domain.Quota{{Resource: "user", Limit: 1}, {Resource: "user", Limit: 2}}
			return p
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mutate(valid).Validate()
			if err == nil {
				t.Fatal("Validate() must fail")
			}
			if tc.wantSent != nil && !errors.Is(err, tc.wantSent) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.wantSent)
			}
		})
	}
}

// TestPlanSortedQuotas 排序返回副本，不修改原切片。
func TestPlanSortedQuotas(t *testing.T) {
	plan := domain.Plan{
		Quotas: []domain.Quota{
			{Resource: "user", Limit: 1},
			{Resource: "api.rps", Limit: 2},
			{Resource: "storage.mb", Limit: 3},
		},
	}
	sorted := plan.SortedQuotas()
	want := []string{"api.rps", "storage.mb", "user"}
	for i, resource := range want {
		if sorted[i].Resource != resource {
			t.Fatalf("SortedQuotas()[%d] = %q, want %q", i, sorted[i].Resource, resource)
		}
	}
	if plan.Quotas[0].Resource != "user" {
		t.Fatal("SortedQuotas() must not mutate the source slice")
	}

	empty := domain.Plan{}
	if got := empty.SortedQuotas(); len(got) != 0 {
		t.Fatalf("SortedQuotas() on empty plan = %v", got)
	}
}

// TestExportManifestValidate 导出证据的登记合法性（D-05 第 2 段的硬前置）。
func TestExportManifestValidate(t *testing.T) {
	sha := strings.Repeat("a1", 32) // 64 位十六进制
	valid := domain.ExportManifest{TenantID: "t-1", ManifestSHA256: sha, CreatedBy: "admin-1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	upper := valid
	upper.ManifestSHA256 = strings.ToUpper(sha)
	if err := upper.Validate(); err != nil {
		t.Fatalf("Validate() must accept uppercase sha: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(domain.ExportManifest) domain.ExportManifest
		want   error
	}{
		{"missing tenant", func(m domain.ExportManifest) domain.ExportManifest { m.TenantID = " "; return m }, domain.ErrManifestTenantRequired},
		{"short sha", func(m domain.ExportManifest) domain.ExportManifest { m.ManifestSHA256 = sha[:63]; return m }, domain.ErrManifestSHAInvalid},
		{"non hex sha", func(m domain.ExportManifest) domain.ExportManifest {
			m.ManifestSHA256 = strings.Repeat("z", 64)
			return m
		}, domain.ErrManifestSHAInvalid},
		{"missing creator", func(m domain.ExportManifest) domain.ExportManifest { m.CreatedBy = ""; return m }, domain.ErrManifestCreatorRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(valid).Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestExportManifestNormalized 归一化：去空白 + SHA 转小写，用于落库前的统一形态。
func TestExportManifestNormalized(t *testing.T) {
	m := domain.ExportManifest{
		TenantID:       " t-1 ",
		ManifestSHA256: "  " + strings.Repeat("AB", 32) + "  ",
		CreatedBy:      " admin-1 ",
		FileCount:      3,
		TotalRows:      42,
	}
	got := m.Normalized()
	if got.TenantID != "t-1" || got.CreatedBy != "admin-1" {
		t.Fatalf("Normalized() must trim ids: %+v", got)
	}
	if got.ManifestSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("Normalized() sha = %q, want lowercase trimmed", got.ManifestSHA256)
	}
	if got.FileCount != 3 || got.TotalRows != 42 {
		t.Fatalf("Normalized() must keep counters: %+v", got)
	}
	if m.TenantID != " t-1 " {
		t.Fatal("Normalized() must return a copy, not mutate the receiver")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Normalized().Validate() error = %v", err)
	}
}

// TestExportManifestString 报告形态：日志与处置报告按此字符串检索租户。
func TestExportManifestString(t *testing.T) {
	m := domain.ExportManifest{
		TenantID:       "t-1",
		ManifestSHA256: strings.Repeat("ab", 32),
		FileCount:      3,
		TotalRows:      42,
		CreatedAt:      time.Time{},
	}
	got := m.String()
	for _, want := range []string{"tenant=t-1", "files=3", "rows=42", strings.Repeat("ab", 32)} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q, want substring %q", got, want)
		}
	}
}

package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
)

// baseFilter 返回一个合法过滤条件；各用例在其上做单点破坏。
func baseFilter() domain.Filter {
	to := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	return domain.Filter{
		From:  to.Add(-24 * time.Hour),
		To:    to,
		Limit: 20,
	}
}

// TestFilterValidate 覆盖时间窗必填、跨度上限与枚举校验（ENT-04 查询契约）。
func TestFilterValidate(t *testing.T) {
	maxRange := 31 * 24 * time.Hour

	tests := []struct {
		name    string
		mutate  func(*domain.Filter)
		wantErr string
	}{
		{name: "valid", mutate: func(*domain.Filter) {}},
		{
			name:    "missing from",
			mutate:  func(f *domain.Filter) { f.From = time.Time{} },
			wantErr: "from and to are required",
		},
		{
			name:    "to before from",
			mutate:  func(f *domain.Filter) { f.To = f.From.Add(-time.Hour) },
			wantErr: "to must be after from",
		},
		{
			name:    "range exceeds max",
			mutate:  func(f *domain.Filter) { f.From = f.To.Add(-40 * 24 * time.Hour) },
			wantErr: "time range exceeds maximum",
		},
		{
			name:    "limit must be positive",
			mutate:  func(f *domain.Filter) { f.Limit = 0 },
			wantErr: "limit must be positive",
		},
		{
			name:    "negative offset",
			mutate:  func(f *domain.Filter) { f.Offset = -1 },
			wantErr: "offset must be >= 0",
		},
		{
			name:    "unsupported outcome",
			mutate:  func(f *domain.Filter) { f.Outcome = "unknown" },
			wantErr: "unsupported outcome",
		},
		{
			name:    "unsupported channel",
			mutate:  func(f *domain.Filter) { f.Channel = "internal" },
			wantErr: "unsupported channel",
		},
		{
			name: "actor id too long",
			mutate: func(f *domain.Filter) {
				f.ActorID = strings.Repeat("a", domain.MaxFilterTextLen+1)
			},
			wantErr: "actor_id exceeds",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter := baseFilter()
			tc.mutate(&filter)
			err := filter.Validate(maxRange)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

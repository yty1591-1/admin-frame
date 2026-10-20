package security

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// TestParseDemoToken 覆盖演示令牌的端别解析与非法格式分支。
func TestParseDemoToken(t *testing.T) {
	cases := []struct {
		token     string
		wantErr   bool
		wantCh    auth.Channel
		wantSub   string
		wantMerch string
	}{
		{token: "admin.u-1", wantCh: auth.ChannelAdmin, wantSub: "u-1"},
		{token: "biz.m-1.u-1", wantCh: auth.ChannelBiz, wantSub: "u-1", wantMerch: "m-1"}, //nolint:gosec // 演示令牌格式样例，非真实凭据。
		{token: "c.u-1", wantCh: auth.ChannelC, wantSub: "u-1"},
		{token: "admin", wantErr: true},
		{token: "admin.", wantErr: true},
		{token: "admin.u-1.extra", wantErr: true},
		{token: "biz.m-1", wantErr: true},
		{token: "biz..u-1", wantErr: true},
		{token: "biz.m-1.", wantErr: true},
		{token: "c.", wantErr: true},
		{token: "unknown.u-1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			p, err := parseDemoToken(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDemoToken(%q) accepted", tc.token)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDemoToken(%q): %v", tc.token, err)
			}
			if p.Channel != tc.wantCh || p.SubjectID != tc.wantSub || p.MerchantID != tc.wantMerch {
				t.Fatalf("principal=%+v", p)
			}
		})
	}
}

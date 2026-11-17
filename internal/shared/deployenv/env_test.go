package deployenv_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/deployenv"
)

func TestIsLocalDev(t *testing.T) {
	t.Parallel()
	cases := []struct {
		env  string
		want bool
	}{
		{"local", true},
		{"dev", true},
		{"test", true},
		{"DEV", true},
		{"staging", false},
		{"prod", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := deployenv.IsLocalDev(tc.env); got != tc.want {
			t.Fatalf("IsLocalDev(%q)=%v want %v", tc.env, got, tc.want)
		}
	}
}

func TestIsProdLike(t *testing.T) {
	t.Parallel()
	if !deployenv.IsProdLike("prod") || !deployenv.IsProdLike("staging") {
		t.Fatal("prod/staging must be prod-like")
	}
	if deployenv.IsProdLike("dev") || deployenv.IsProdLike("") {
		t.Fatal("dev/empty must not be prod-like")
	}
}

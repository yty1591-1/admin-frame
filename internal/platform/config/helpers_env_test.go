package config

import "testing"

func TestIsLocalDevEnv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		env  string
		want bool
	}{
		{"local", true},
		{"dev", true},
		{"test", true},
		{"LOCAL", true},
		{" Dev ", true},
		{"staging", false},
		{"prod", false},
		{"", false},
		{"production", false},
	}
	for _, tc := range cases {
		if got := IsLocalDevEnv(tc.env); got != tc.want {
			t.Fatalf("IsLocalDevEnv(%q)=%v want %v", tc.env, got, tc.want)
		}
	}
}

func TestIsProdLikeEnv(t *testing.T) {
	t.Parallel()
	if !IsProdLikeEnv("prod") || !IsProdLikeEnv("staging") {
		t.Fatal("prod/staging should be prod-like")
	}
	if IsProdLikeEnv("dev") || IsProdLikeEnv("") {
		t.Fatal("dev/empty must not be prod-like")
	}
}

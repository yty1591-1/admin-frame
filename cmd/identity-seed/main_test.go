package main

import (
	"strings"
	"testing"
)

// TestParseChannel 覆盖可开通端别与 C 端拒绝。
func TestParseChannel(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "admin", want: "admin"},
		{raw: " BIZ ", want: "biz"},
		{raw: "c", wantErr: true},
		{raw: "worker", wantErr: true},
		{raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseChannel(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseChannel(%q) = %q, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChannel(%q): %v", tc.raw, err)
			}
			if string(got) != tc.want {
				t.Fatalf("parseChannel(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestSQLDriver 覆盖驱动别名与 memory 拒绝。
func TestSQLDriver(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "mysql", want: "mysql"},
		{raw: "MariaDB", want: "mysql"},
		{raw: "postgres", want: "pgx"},
		{raw: "pgx", want: "pgx"},
		{raw: "memory", wantErr: true},
		{raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := sqlDriver(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("sqlDriver(%q) = %q, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sqlDriver(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("sqlDriver(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestReadPassword 覆盖 stdin 去尾换行与环境变量回退。
func TestReadPassword(t *testing.T) {
	t.Run("stdin trims trailing newline", func(t *testing.T) {
		got, err := readPassword(true, strings.NewReader("Correct#Passw0rd\r\n"))
		if err != nil {
			t.Fatalf("readPassword: %v", err)
		}
		if got != "Correct#Passw0rd" {
			t.Fatalf("password = %q, want trimmed value", got)
		}
	})

	t.Run("stdin keeps inner whitespace", func(t *testing.T) {
		got, err := readPassword(true, strings.NewReader(" pass word "))
		if err != nil {
			t.Fatalf("readPassword: %v", err)
		}
		if got != " pass word " {
			t.Fatalf("password = %q, want unchanged inner whitespace", got)
		}
	})

	t.Run("empty stdin rejected", func(t *testing.T) {
		if _, err := readPassword(true, strings.NewReader("\n")); err == nil {
			t.Fatal("empty stdin password must be rejected")
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		t.Setenv(passwordEnv, "env-secret")
		got, err := readPassword(false, strings.NewReader(""))
		if err != nil {
			t.Fatalf("readPassword: %v", err)
		}
		if got != "env-secret" {
			t.Fatalf("password = %q", got)
		}
	})

	t.Run("missing source rejected", func(t *testing.T) {
		t.Setenv(passwordEnv, "")
		if _, err := readPassword(false, strings.NewReader("")); err == nil {
			t.Fatal("missing password source must be rejected")
		}
	})
}

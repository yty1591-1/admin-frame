package migrate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

func TestSplitStatements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "two statements",
			in:   "CREATE TABLE a (id INT);\nINSERT INTO a VALUES (1);",
			want: []string{"CREATE TABLE a (id INT)", "INSERT INTO a VALUES (1)"},
		},
		{
			name: "semicolon in string",
			in:   "INSERT INTO t VALUES ('a;b');",
			want: []string{"INSERT INTO t VALUES ('a;b')"},
		},
		{
			name: "doubled quote",
			in:   "SELECT 'it''s; fine'; SELECT 2;",
			want: []string{"SELECT 'it''s; fine'", "SELECT 2"},
		},
		{
			name: "line comment",
			in:   "-- a;b\nSELECT 1;",
			want: []string{"-- a;b\nSELECT 1"},
		},
		{
			name: "nested block comment",
			in:   "/* a /* b; */ c; */ SELECT 1;",
			want: []string{"/* a /* b; */ c; */ SELECT 1"},
		},
		{
			name: "dollar quote",
			in:   "DO $$ BEGIN RAISE NOTICE 'x;y'; END $$;\nSELECT 1;",
			want: []string{"DO $$ BEGIN RAISE NOTICE 'x;y'; END $$", "SELECT 1"},
		},
		{
			name: "tagged dollar quote",
			in:   "CREATE FUNCTION f() RETURNS void AS $body$ SELECT 1; $body$ LANGUAGE sql;",
			want: []string{"CREATE FUNCTION f() RETURNS void AS $body$ SELECT 1; $body$ LANGUAGE sql"},
		},
		{
			name: "backtick identifier",
			in:   "CREATE TABLE `a;b` (id INT);",
			want: []string{"CREATE TABLE `a;b` (id INT)"},
		},
		{
			name: "only comments",
			in:   "-- nothing here\n/* and here */\n",
			want: nil,
		},
		{
			name: "trailing whitespace and blank statements",
			in:   " ; ;\nSELECT 1 ; ;",
			want: []string{"SELECT 1"},
		},
		{
			name: "no trailing semicolon",
			in:   "SELECT 1",
			want: []string{"SELECT 1"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := migrate.SplitStatements(tc.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d statements %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("statement %d=%q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSplitStatementsMalformed(t *testing.T) {
	t.Parallel()
	cases := []string{
		"SELECT 'unterminated;",
		`SELECT "unterminated;`,
		"SELECT `unterminated;",
		"SELECT 1; /* unterminated",
		"DO $$ SELECT 1;",
	}
	for _, in := range cases {
		if _, err := migrate.SplitStatements(in); !errors.Is(err, migrate.ErrMalformedSQL) {
			t.Fatalf("input %q: err=%v, want ErrMalformedSQL", in, err)
		}
	}
}

func TestSplitStatementsKeepsCommentsWithStatement(t *testing.T) {
	t.Parallel()
	got, err := migrate.SplitStatements("-- IRREVERSIBLE: ADR-0001\nDO $$ BEGIN RAISE EXCEPTION 'x'; END $$;")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "-- IRREVERSIBLE") {
		t.Fatalf("got=%q", got)
	}
}

package migrate

import (
	"strings"
	"testing"
)

func TestSplitStatementsMySQLBackslashEscapes(t *testing.T) {
	t.Parallel()
	script := `INSERT INTO t VALUES ('a\';b'); SELECT 1;`

	got, err := splitStatements(script, true)
	if err != nil {
		t.Fatalf("mysql split: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("mysql split got %d statements %q, want 2", len(got), got)
	}

	if _, err := splitStatements(script, false); err == nil {
		t.Fatal("ansi split should treat quote after backslash as terminator and fail")
	}
}

func TestSplitStatementsMySQLHashComment(t *testing.T) {
	t.Parallel()
	got, err := splitStatements("# comment; with semicolon\nSELECT 1;", true)
	if err != nil {
		t.Fatalf("mysql hash comment: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "SELECT 1") {
		t.Fatalf("got=%q", got)
	}
}

package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresDriverAndDSN(t *testing.T) {
	t.Setenv("ADMIN_FRAME_DB_DRIVER", "")
	t.Setenv("ADMIN_FRAME_DB_DSN", "")

	if err := run(context.Background(), options{}); err == nil || !strings.Contains(err.Error(), "driver") {
		t.Fatalf("empty opts err=%v", err)
	}
	if err := run(context.Background(), options{Driver: "memory", DSN: "x"}); err == nil || !strings.Contains(err.Error(), "not a database/sql driver") {
		t.Fatalf("memory driver err=%v", err)
	}
	if err := run(context.Background(), options{Driver: "postgres"}); err == nil || !strings.Contains(err.Error(), "dsn") {
		t.Fatalf("missing dsn err=%v", err)
	}
	if err := run(context.Background(), options{Driver: "not-a-dialect", DSN: "x"}); err == nil {
		t.Fatal("unsupported dialect should fail")
	}
	if err := run(context.Background(), options{Driver: "sqlite3", DSN: "x"}); err == nil || !strings.Contains(err.Error(), "not supported by this binary") {
		t.Fatalf("sqlite driver err=%v", err)
	}
}

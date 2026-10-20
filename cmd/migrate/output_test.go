package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

func TestPrintStatusReportsDriftWithoutPending(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, migrate.Status{
		Current: 2,
		Applied: []migrate.Record{
			{Version: 1, Description: "create_orders", AppliedAt: time.Unix(0, 0).UTC()},
			{Version: 2, Description: "add_name", AppliedAt: time.Unix(0, 0).UTC(), Dirty: true},
		},
		Dirty:   true,
		Drift:   []int64{2},
		Missing: []int64{9},
	})

	out := buf.String()
	for _, want := range []string{
		"current version: 000002",
		"dirty: true",
		"applied: 2",
		"pending: none",
		"000002_add_name",
		"dirty",
		"drift (checksum mismatch): [2]",
		"missing from source: [9]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintStatusWithPending(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, migrate.Status{
		Current: 1,
		Pending: []migrate.Migration{{Version: 2, Description: "add_name"}},
	})
	out := buf.String()
	if !strings.Contains(out, "pending:\n  000002_add_name\n") {
		t.Fatalf("pending not rendered:\n%s", out)
	}
}

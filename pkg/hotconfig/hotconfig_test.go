package hotconfig_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/hotconfig"
)

var specs = []hotconfig.Spec{
	{Key: "rate_limit.rps", Type: hotconfig.TypeInt},
	{Key: "rate_limit.degrade_on_failure", Type: hotconfig.TypeBool},
	{Key: "worker.poll_interval", Type: hotconfig.TypeDuration},
	{Key: "log.level", Type: hotconfig.TypeString, Enum: []string{"debug", "info", "warn", "error"}},
}

func TestBuildNormalizesAndIgnoresUnknownKeys(t *testing.T) {
	now := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	view, err := hotconfig.Build(specs, map[string]string{
		"rate_limit.rps":       " 120 ",
		"log.level":            "debug",
		"worker.poll_interval": "90s",
		"smtp.password":        "must-not-enter-view", // 未声明，必须被忽略
	}, 3, 42, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := view.Int("rate_limit.rps", 0); got != 120 {
		t.Fatalf("rps = %d, want 120", got)
	}
	if got := view.Duration("worker.poll_interval", 0); got != 90*time.Second {
		t.Fatalf("poll_interval = %v", got)
	}
	if got := view.String("log.level", "info"); got != "debug" {
		t.Fatalf("log.level = %q", got)
	}
	if _, ok := view.Lookup("smtp.password"); ok {
		t.Fatal("undeclared key must not enter the view")
	}
	if view.Version() != 3 || view.ReleaseID() != 42 || !view.BuiltAt().Equal(now) {
		t.Fatalf("metadata mismatch: %+v", view)
	}
}

func TestBuildRejectsInvalidValue(t *testing.T) {
	cases := []map[string]string{
		{"rate_limit.rps": "abc"},
		{"rate_limit.degrade_on_failure": "maybe"},
		{"worker.poll_interval": "5"},
		{"log.level": "trace"},
	}
	for _, values := range cases {
		if _, err := hotconfig.Build(specs, values, 1, 1, time.Now()); err == nil {
			t.Fatalf("Build(%v) error = nil, want rejection", values)
		}
	}
}

func TestHolderSkipsIdenticalFingerprint(t *testing.T) {
	first, err := hotconfig.Build(specs, map[string]string{"rate_limit.rps": "100"}, 1, 1, time.Now())
	if err != nil {
		t.Fatalf("build first: %v", err)
	}
	holder := hotconfig.NewHolder(first)
	if holder.Current().Int("rate_limit.rps", 0) != 100 {
		t.Fatal("initial view not installed")
	}

	same, err := hotconfig.Build(specs, map[string]string{"rate_limit.rps": "100"}, 2, 2, time.Now())
	if err != nil {
		t.Fatalf("build same: %v", err)
	}
	if holder.Store(same) {
		t.Fatal("identical values must not replace the view")
	}
	if holder.Current().Version() != 1 {
		t.Fatalf("version = %d, want 1 (unchanged)", holder.Current().Version())
	}

	changed, err := hotconfig.Build(specs, map[string]string{"rate_limit.rps": "200"}, 2, 2, time.Now())
	if err != nil {
		t.Fatalf("build changed: %v", err)
	}
	if !holder.Store(changed) {
		t.Fatal("changed values must replace the view")
	}
	if holder.Current().Int("rate_limit.rps", 0) != 200 {
		t.Fatal("new view not visible")
	}
}

func TestViewMissesFallBackToDefaults(t *testing.T) {
	view, err := hotconfig.Build(specs, nil, 1, 1, time.Now())
	if err != nil {
		t.Fatalf("build empty: %v", err)
	}
	if view.Int("rate_limit.rps", 7) != 7 ||
		view.Bool("rate_limit.degrade_on_failure", true) != true ||
		view.Duration("worker.poll_interval", time.Second) != time.Second ||
		view.String("log.level", "info") != "info" {
		t.Fatal("missing keys must fall back to caller defaults")
	}
	if len(view.Keys()) != 0 {
		t.Fatalf("empty view keys = %v", view.Keys())
	}
}

func TestNilHolderIsSafe(t *testing.T) {
	var holder *hotconfig.Holder
	if holder.Current() != nil {
		t.Fatal("nil holder must return nil view")
	}
	if holder.Store(nil) {
		t.Fatal("nil holder must not report replacement")
	}

	var view *hotconfig.View
	if view.Int("rate_limit.rps", 5) != 5 || view.Fingerprint() != "" {
		t.Fatal("nil view must be safe and return defaults")
	}
}

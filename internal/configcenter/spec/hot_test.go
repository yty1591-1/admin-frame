package spec_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// TestHotSpecsResolveToDeclarations 守护 ADR-0015 白名单与键声明一致。
func TestHotSpecsResolveToDeclarations(t *testing.T) {
	specs, err := spec.HotSpecs()
	if err != nil {
		t.Fatalf("HotSpecs: %v", err)
	}
	if len(specs) != len(spec.HotKeys) {
		t.Fatalf("HotSpecs returned %d specs, want %d", len(specs), len(spec.HotKeys))
	}
	seen := make(map[string]bool, len(specs))
	for _, entry := range specs {
		if entry.Key == "" || entry.Type == "" {
			t.Fatalf("incomplete hot spec: %+v", entry)
		}
		if seen[entry.Key] {
			t.Fatalf("duplicate hot key %s", entry.Key)
		}
		seen[entry.Key] = true
	}
}

// TestHotKeysExcludeConcurrency 守护 ADR-0015 决策 7：并发度不热更。
func TestHotKeysExcludeConcurrency(t *testing.T) {
	for _, key := range []string{"async.outbox.concurrency", "async.consumer.concurrency"} {
		if _, ok := spec.HotKeySet()[key]; ok {
			t.Fatalf("key %s must not be hot: worker pool resize is out of M1 scope", key)
		}
	}
}

// TestHotKeysExcludeWorkerPollInterval 守护 ADR-0015 H5：无消费点的键不得留在白名单。
func TestHotKeysExcludeWorkerPollInterval(t *testing.T) {
	if _, ok := spec.HotKeySet()["worker.poll_interval"]; ok {
		t.Fatal("worker.poll_interval must not be hot: no consumer loop exists")
	}
	registry, err := spec.Platform()
	if err != nil {
		t.Fatalf("Platform: %v", err)
	}
	keySpec, ok := registry.Lookup("platform", "worker.poll_interval")
	if !ok {
		t.Fatal("worker.poll_interval must remain declared as a startup key")
	}
	if keySpec.Reloadable {
		t.Fatal("worker.poll_interval Reloadable must be false after H5")
	}
}

// TestHotKeySetMatchesList 守护集合与切片的对应关系。
func TestHotKeySetMatchesList(t *testing.T) {
	set := spec.HotKeySet()
	if len(set) != len(spec.HotKeys) {
		t.Fatalf("set size %d != list size %d", len(set), len(spec.HotKeys))
	}
	for _, key := range spec.HotKeys {
		if _, ok := set[key]; !ok {
			t.Fatalf("key %s missing from set", key)
		}
	}
}

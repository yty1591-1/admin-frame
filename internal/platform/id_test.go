package platform

import (
	"testing"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// TestResolveNodeID 覆盖显式节点号与 StatefulSet 序号推导。
func TestResolveNodeID(t *testing.T) {
	nodeID := int64(7)
	cases := []struct {
		name     string
		cfg      platformconfig.IDConfig
		hostname string
		want     int64
		wantErr  bool
	}{
		{name: "explicit", cfg: platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceConfig, NodeID: &nodeID}, want: 7},
		{name: "default zero", cfg: platformconfig.IDConfig{}, want: 0},
		{name: "statefulset", cfg: platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceStatefulSet}, hostname: "admin-api-3", want: 3},
		{name: "statefulset multi digit", cfg: platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceStatefulSet}, hostname: "biz-api-12", want: 12},
		{name: "statefulset missing suffix", cfg: platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceStatefulSet}, hostname: "admin-api", wantErr: true},
		{name: "statefulset out of range", cfg: platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceStatefulSet}, hostname: "admin-api-1024", wantErr: true},
		{name: "unsupported source", cfg: platformconfig.IDConfig{NodeIDSource: "registry"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveNodeID(tc.cfg, tc.hostname)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveNodeID(%+v, %q) = %d, want error", tc.cfg, tc.hostname, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNodeID: %v", err)
			}
			if got != tc.want {
				t.Fatalf("nodeID = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestNewIDGeneratorGeneratesUniqueIDs 验证同一节点号下 ID 严格递增。
func TestNewIDGeneratorGeneratesUniqueIDs(t *testing.T) {
	nodeID := int64(5)
	generator, got, err := NewIDGenerator(platformconfig.IDConfig{NodeIDSource: platformconfig.IDSourceConfig, NodeID: &nodeID})
	if err != nil {
		t.Fatalf("NewIDGenerator: %v", err)
	}
	if got != 5 {
		t.Fatalf("nodeID = %d, want 5", got)
	}
	seen := make(map[int64]bool, 1000)
	var previous int64
	for i := 0; i < 1000; i++ {
		value, nextErr := generator.Next()
		if nextErr != nil {
			t.Fatalf("Next: %v", nextErr)
		}
		if seen[value] {
			t.Fatalf("duplicate id %d", value)
		}
		if value <= previous {
			t.Fatalf("id %d is not increasing after %d", value, previous)
		}
		seen[value] = true
		previous = value
	}
}

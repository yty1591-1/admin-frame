package domain

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func validItem() Item {
	return Item{
		ID:          1,
		Env:         EnvDev,
		Namespace:   "platform",
		Key:         "http.timeout",
		ValueType:   ValueTypeDuration,
		Description: "HTTP 超时",
		Owner:       "team-platform",
		Status:      ItemStatusDraft,
		Version:     1,
		CreatedBy:   "admin",
		UpdatedBy:   "admin",
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
	}
}

func validRelease() Release {
	return Release{
		ID:           1,
		Env:          EnvDev,
		Namespace:    "platform",
		ReleaseNo:    1,
		Name:         "baseline",
		Status:       ReleaseStatusDraft,
		Strategy:     StrategyAll,
		SnapshotHash: strings.Repeat("a", 64),
		CreatedBy:    "admin",
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	}
}

func TestItemValidate(t *testing.T) {
	if err := validItem().Validate(); err != nil {
		t.Fatalf("valid item rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Item)
	}{
		{"missing id", func(i *Item) { i.ID = 0 }},
		{"invalid env", func(i *Item) { i.Env = "production" }},
		{"invalid namespace", func(i *Item) { i.Namespace = "Platform" }},
		{"invalid key", func(i *Item) { i.Key = "SingleSegment" }},
		{"invalid value type", func(i *Item) { i.ValueType = "float" }},
		{"missing owner", func(i *Item) { i.Owner = "  " }},
		{"invalid status", func(i *Item) { i.Status = "published" }},
		{"negative current version", func(i *Item) { i.CurrentVersion = -1 }},
		{"zero version", func(i *Item) { i.Version = 0 }},
		{"invalid validation json", func(i *Item) { i.ValidationJSON = "{not-json}" }},
		{"missing created by", func(i *Item) { i.CreatedBy = "" }},
		{"missing timestamps", func(i *Item) { i.CreatedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := validItem()
			tc.mutate(&item)
			if err := item.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestItemStatusTransitions(t *testing.T) {
	allowed := []struct{ from, to ItemStatus }{
		{ItemStatusDraft, ItemStatusActive},
		{ItemStatusDraft, ItemStatusArchived},
		{ItemStatusActive, ItemStatusDeprecated},
		{ItemStatusDeprecated, ItemStatusActive},
		{ItemStatusDeprecated, ItemStatusArchived},
	}
	for _, tc := range allowed {
		if !tc.from.CanTransitionTo(tc.to) {
			t.Fatalf("%s -> %s should be allowed", tc.from, tc.to)
		}
	}
	denied := []struct{ from, to ItemStatus }{
		{ItemStatusArchived, ItemStatusActive},
		{ItemStatusActive, ItemStatusDraft},
		{ItemStatusDraft, ItemStatusDeprecated},
	}
	for _, tc := range denied {
		if tc.from.CanTransitionTo(tc.to) {
			t.Fatalf("%s -> %s should be denied", tc.from, tc.to)
		}
	}
}

func TestItemVersionValidate(t *testing.T) {
	version := ItemVersion{
		ID:        1,
		ItemID:    1,
		Version:   1,
		ValueText: "10s",
		ValueHash: HashValue("10s"),
		CreatedBy: "admin",
		CreatedAt: testNow,
	}
	if err := version.Validate(); err != nil {
		t.Fatalf("valid version rejected: %v", err)
	}

	mismatch := version
	mismatch.ValueHash = HashValue("20s")
	if err := mismatch.Validate(); err == nil {
		t.Fatal("value_hash mismatch must be rejected")
	}

	badHash := version
	badHash.ValueHash = "ABC"
	if err := badHash.Validate(); err == nil {
		t.Fatal("non sha256 hash must be rejected")
	}
}

func TestReleaseValidate(t *testing.T) {
	if err := validRelease().Validate(); err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}

	withRule := validRelease()
	withRule.Strategy = StrategyTenantList
	if err := withRule.Validate(); err == nil {
		t.Fatal("non-all strategy without target_rule_json must be rejected")
	}
	withRule.TargetRuleJSON = `{"tenant_ids":["t-1"]}`
	if err := withRule.Validate(); err != nil {
		t.Fatalf("valid tenant_list release rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Release)
	}{
		{"missing id", func(r *Release) { r.ID = 0 }},
		{"zero release no", func(r *Release) { r.ReleaseNo = 0 }},
		{"missing name", func(r *Release) { r.Name = "" }},
		{"invalid status", func(r *Release) { r.Status = "live" }},
		{"invalid strategy", func(r *Release) { r.Strategy = "expr" }},
		{"invalid snapshot hash", func(r *Release) { r.SnapshotHash = "short" }},
		{"negative snapshot size", func(r *Release) { r.SnapshotSize = -1 }},
		{"missing created by", func(r *Release) { r.CreatedBy = " " }},
		{"invalid rule json", func(r *Release) {
			r.Strategy = StrategyPercentage
			r.TargetRuleJSON = "{"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := validRelease()
			tc.mutate(&release)
			if err := release.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestReleaseStatusTransitions(t *testing.T) {
	allowed := []struct{ from, to ReleaseStatus }{
		{ReleaseStatusDraft, ReleaseStatusPendingReview},
		{ReleaseStatusPendingReview, ReleaseStatusApproved},
		{ReleaseStatusPendingReview, ReleaseStatusRejected},
		{ReleaseStatusApproved, ReleaseStatusActive},
		{ReleaseStatusActive, ReleaseStatusSuperseded},
		{ReleaseStatusActive, ReleaseStatusRolledBack},
	}
	for _, tc := range allowed {
		if !tc.from.CanTransitionTo(tc.to) {
			t.Fatalf("%s -> %s should be allowed", tc.from, tc.to)
		}
	}
	denied := []struct{ from, to ReleaseStatus }{
		{ReleaseStatusDraft, ReleaseStatusActive},
		{ReleaseStatusApproved, ReleaseStatusSuperseded},
		{ReleaseStatusRejected, ReleaseStatusActive},
		{ReleaseStatusSuperseded, ReleaseStatusActive},
	}
	for _, tc := range denied {
		if tc.from.CanTransitionTo(tc.to) {
			t.Fatalf("%s -> %s should be denied", tc.from, tc.to)
		}
	}
}

func TestReleaseItemValidate(t *testing.T) {
	item := ReleaseItem{
		ReleaseID:   1,
		ItemID:      2,
		ItemVersion: 3,
		ConfigKey:   "http.timeout",
		ValueHash:   HashValue("10s"),
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("valid release item rejected: %v", err)
	}

	badKey := item
	badKey.ConfigKey = "http"
	if err := badKey.Validate(); err == nil {
		t.Fatal("invalid config key must be rejected")
	}

	badHash := item
	badHash.ValueHash = ""
	if err := badHash.Validate(); err == nil {
		t.Fatal("empty value hash must be rejected")
	}
}

func TestReleaseReviewValidate(t *testing.T) {
	review := ReleaseReview{
		ID:        1,
		ReleaseID: 1,
		Reviewer:  "reviewer",
		Decision:  DecisionApproved,
		CreatedAt: testNow,
	}
	if err := review.Validate(); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}

	badDecision := review
	badDecision.Decision = "abstain"
	if err := badDecision.Validate(); err == nil {
		t.Fatal("invalid decision must be rejected")
	}

	missingReviewer := review
	missingReviewer.Reviewer = ""
	if err := missingReviewer.Validate(); err == nil {
		t.Fatal("missing reviewer must be rejected")
	}
}

func TestPointerValidate(t *testing.T) {
	pointer := Pointer{
		Env:             EnvDev,
		Namespace:       "platform",
		ActiveReleaseID: 1,
		Version:         1,
		UpdatedAt:       testNow,
	}
	if err := pointer.Validate(); err != nil {
		t.Fatalf("valid pointer rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Pointer)
	}{
		{"invalid env", func(p *Pointer) { p.Env = "" }},
		{"invalid namespace", func(p *Pointer) { p.Namespace = "-bad" }},
		{"missing active release", func(p *Pointer) { p.ActiveReleaseID = 0 }},
		{"zero version", func(p *Pointer) { p.Version = 0 }},
		{"missing updated at", func(p *Pointer) { p.UpdatedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pointer := pointer
			tc.mutate(&pointer)
			if err := pointer.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestHashValue(t *testing.T) {
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	got := HashValue("")
	if got != emptySHA256 {
		t.Fatalf("empty value hash mismatch: got %q", got)
	}
	if !ValidSHA256(got) {
		t.Fatalf("hash must be 64 lowercase hex, got %q", got)
	}
	if HashValue("a") == HashValue("b") {
		t.Fatal("different values must not collide")
	}
}

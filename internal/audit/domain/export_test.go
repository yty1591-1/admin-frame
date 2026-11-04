package domain_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
)

var exportNow = time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)

// TestExportStatusMatrix 导出状态的两组判定：合法取值与终态。
//
// 终态决定 worker 是否会重新抢占：把 succeeded 误判成非终态会导致重复生成文件，
// 把 running 误判成终态会让崩溃任务的记录永久卡死。
func TestExportStatusMatrix(t *testing.T) {
	cases := []struct {
		status   domain.ExportStatus
		valid    bool
		terminal bool
	}{
		{domain.ExportPending, true, false},
		{domain.ExportRunning, true, false},
		{domain.ExportSucceeded, true, true},
		{domain.ExportFailed, true, true},
		{domain.ExportExpired, true, true},
		{"", false, false},
		{"cancelled", false, false},
	}
	for _, tc := range cases {
		if got := tc.status.Valid(); got != tc.valid {
			t.Fatalf("ExportStatus(%q).Valid() = %v, want %v", tc.status, got, tc.valid)
		}
		if got := tc.status.Terminal(); got != tc.terminal {
			t.Fatalf("ExportStatus(%q).Terminal() = %v, want %v", tc.status, got, tc.terminal)
		}
	}
}

// TestSnapshotRoundTrip 过滤条件持久化往返：时间统一 UTC，分页字段不入库。
func TestSnapshotRoundTrip(t *testing.T) {
	local := time.FixedZone("UTC+8", 8*3600)
	filter := domain.Filter{
		From:         time.Date(2026, 11, 4, 8, 0, 0, 0, local),
		To:           time.Date(2026, 11, 4, 18, 0, 0, 0, local),
		ActorID:      "admin-1",
		Action:       "tenant.create",
		Channel:      "admin",
		Outcome:      "success",
		MerchantID:   "m-1",
		ResourceType: "tenant",
		ResourceID:   "t-1",
		TraceID:      "trace-1",
		Limit:        500,
		Offset:       40,
	}
	snapshot := domain.Snapshot(filter)
	if snapshot.From.Location() != time.UTC || snapshot.To.Location() != time.UTC {
		t.Fatalf("Snapshot() must normalize to UTC: %+v", snapshot)
	}
	if !snapshot.From.Equal(filter.From) || !snapshot.To.Equal(filter.To) {
		t.Fatal("Snapshot() must preserve the instant, not shift it")
	}

	restored := snapshot.Filter(100)
	if restored.Limit != 100 || restored.Offset != 0 {
		t.Fatalf("Filter() must take limit from caller and drop offset: %+v", restored)
	}
	if restored.ActorID != filter.ActorID || restored.TraceID != filter.TraceID ||
		restored.MerchantID != filter.MerchantID || restored.ResourceType != filter.ResourceType ||
		restored.ResourceID != filter.ResourceID || restored.Action != filter.Action ||
		restored.Channel != filter.Channel || restored.Outcome != filter.Outcome {
		t.Fatalf("Filter() lost fields: %+v", restored)
	}
	if !restored.From.Equal(filter.From) || !restored.To.Equal(filter.To) {
		t.Fatal("Filter() must preserve the time window")
	}
}

// TestExportJobDownloadable 下载资格：仅 succeeded 且在保留期内。
func TestExportJobDownloadable(t *testing.T) {
	job := domain.ExportJob{
		ID:        "job-1",
		Status:    domain.ExportSucceeded,
		ExpiresAt: exportNow.Add(time.Hour),
	}
	if !job.Downloadable(exportNow) {
		t.Fatal("succeeded job before expiry must be downloadable")
	}
	// 到期时刻本身不可下载：避免「保留期最后 1 秒」的竞态争议。
	if job.Downloadable(job.ExpiresAt) {
		t.Fatal("job at expiry must not be downloadable")
	}
	if job.Downloadable(job.ExpiresAt.Add(-time.Nanosecond)) == false {
		t.Fatal("job just before expiry must be downloadable")
	}

	for _, status := range []domain.ExportStatus{domain.ExportPending, domain.ExportRunning, domain.ExportFailed, domain.ExportExpired} {
		other := job
		other.Status = status
		if other.Downloadable(exportNow) {
			t.Fatalf("status %q must not be downloadable", status)
		}
	}
	noExpiry := job
	noExpiry.ExpiresAt = time.Time{}
	if noExpiry.Downloadable(exportNow) {
		t.Fatal("job without expiry must not be downloadable")
	}
}

// TestExportJobExpired 保留期判定与状态无关；零值视为「从未设置」，不算过期。
func TestExportJobExpired(t *testing.T) {
	job := domain.ExportJob{ExpiresAt: exportNow}
	for _, status := range []domain.ExportStatus{domain.ExportPending, domain.ExportSucceeded, domain.ExportExpired} {
		probe := job
		probe.Status = status
		if !probe.Expired(exportNow) {
			t.Fatalf("status %q must be expired at ExpiresAt", status)
		}
	}
	if job.Expired(exportNow.Add(-time.Second)) {
		t.Fatal("job must not be expired before ExpiresAt")
	}
	if (domain.ExportJob{}).Expired(exportNow) {
		t.Fatal("zero ExpiresAt must never be considered expired")
	}
}

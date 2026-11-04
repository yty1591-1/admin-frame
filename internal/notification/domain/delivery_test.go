package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var deliveryNow = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

// TestDeliveryStatusMatrix 投递状态的三组判定（合法 / 已受理 / 终态 / 已了结）。
//
// 这张表是编排器「是否还要重试」的唯一依据：Settled 必须覆盖 accepted，
// 否则无回执渠道会被无限重投；Terminal 必须排除 accepted，否则回执没有落点。
func TestDeliveryStatusMatrix(t *testing.T) {
	cases := []struct {
		status   DeliveryStatus
		valid    bool
		sent     bool
		terminal bool
		settled  bool
	}{
		{DeliveryPending, true, false, false, false},
		{DeliveryRetrying, true, false, false, false},
		{DeliveryAccepted, true, true, false, true},
		{DeliveryFailed, true, false, true, true},
		{DeliveryDelivered, true, true, true, true},
		{DeliveryUndelivered, true, true, true, true},
		{"", false, false, false, false},
		{"sending", false, false, false, false},
	}
	for _, tc := range cases {
		if got := tc.status.Valid(); got != tc.valid {
			t.Fatalf("DeliveryStatus(%q).Valid() = %v, want %v", tc.status, got, tc.valid)
		}
		if got := tc.status.Sent(); got != tc.sent {
			t.Fatalf("DeliveryStatus(%q).Sent() = %v, want %v", tc.status, got, tc.sent)
		}
		if got := tc.status.Terminal(); got != tc.terminal {
			t.Fatalf("DeliveryStatus(%q).Terminal() = %v, want %v", tc.status, got, tc.terminal)
		}
		if got := tc.status.Settled(); got != tc.settled {
			t.Fatalf("DeliveryStatus(%q).Settled() = %v, want %v", tc.status, got, tc.settled)
		}
	}
}

// TestDeliveryTransitionsAllowed 逐条锁定状态机白名单：新增边必须过评审。
func TestDeliveryTransitionsAllowed(t *testing.T) {
	allowed := map[DeliveryStatus][]DeliveryStatus{
		DeliveryPending:  {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
		DeliveryRetrying: {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
		DeliveryAccepted: {DeliveryDelivered, DeliveryUndelivered},
	}
	statuses := []DeliveryStatus{
		DeliveryPending, DeliveryRetrying, DeliveryAccepted,
		DeliveryFailed, DeliveryDelivered, DeliveryUndelivered,
	}
	for _, from := range statuses {
		for _, to := range statuses {
			want := false
			for _, target := range allowed[from] {
				if target == to {
					want = true
					break
				}
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Fatalf("CanTransitionTo(%s -> %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	// 终态吸收：failed / delivered / undelivered 不得回到任何状态。
	for _, from := range []DeliveryStatus{DeliveryFailed, DeliveryDelivered, DeliveryUndelivered} {
		if len(DeliveryTransitions()[from]) != 0 {
			t.Fatalf("%s must be absorbing, got %v", from, DeliveryTransitions()[from])
		}
	}
}

// TestDeliveryTransitionsSnapshotIsCopy 快照必须是副本：调用方改它不影响状态机。
func TestDeliveryTransitionsSnapshotIsCopy(t *testing.T) {
	snapshot := DeliveryTransitions()
	snapshot[DeliveryPending] = nil
	snapshot[DeliveryAccepted][0] = DeliveryFailed

	if len(DeliveryTransitions()[DeliveryPending]) != 3 {
		t.Fatal("mutating snapshot must not affect the internal transition table")
	}
	if DeliveryTransitions()[DeliveryAccepted][0] != DeliveryDelivered {
		t.Fatal("mutating snapshot targets must not affect the internal table")
	}
}

// TestNewDelivery 构造期校验：ID / 通知单 / 租户必填，预留渠道拒绝。
func TestNewDelivery(t *testing.T) {
	d, err := NewDelivery(" d-1 ", " r-1 ", " t-1 ", ChannelEmail, deliveryNow)
	if err != nil {
		t.Fatalf("NewDelivery() error = %v", err)
	}
	if d.ID != "d-1" || d.RequestID != "r-1" || d.TenantID != "t-1" {
		t.Fatalf("NewDelivery() must trim ids: %+v", d)
	}
	if d.Status != DeliveryPending || d.Attempt != 1 {
		t.Fatalf("NewDelivery() must start pending/attempt=1: %+v", d)
	}
	if !d.CreatedAt.Equal(deliveryNow) || !d.UpdatedAt.Equal(deliveryNow) {
		t.Fatalf("NewDelivery() timestamps = %v / %v", d.CreatedAt, d.UpdatedAt)
	}

	cases := []struct {
		name    string
		id      string
		request string
		tenant  string
		channel Channel
		want    error
	}{
		{"empty id", " ", "r-1", "t-1", ChannelEmail, nil},
		{"empty request", "d-1", "", "t-1", ChannelEmail, nil},
		{"empty tenant", "d-1", "r-1", " ", ChannelEmail, ErrTenantIDRequired},
		{"reserved channel", "d-1", "r-1", "t-1", ChannelInbox, nil},
		{"unknown channel", "d-1", "r-1", "t-1", "fax", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDelivery(tc.id, tc.request, tc.tenant, tc.channel, deliveryNow)
			if err == nil {
				t.Fatal("NewDelivery() must fail")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("NewDelivery() error = %v, want %v", err, tc.want)
			}
			if tc.name == "reserved channel" {
				var notImpl *ChannelNotImplementedError
				if !errors.As(err, &notImpl) || notImpl.Channel != ChannelInbox {
					t.Fatalf("NewDelivery() error = %v, want ChannelNotImplementedError", err)
				}
			}
		})
	}
}

// TestDeliveryValidate 回读校验：retrying 必须有下次重试时间，终态必须有结束时间。
func TestDeliveryValidate(t *testing.T) {
	valid := Delivery{
		ID:        "d-1",
		RequestID: "r-1",
		TenantID:  "t-1",
		Channel:   ChannelEmail,
		Status:    DeliveryPending,
		Attempt:   1,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	nextRetry := deliveryNow
	finished := deliveryNow
	retrying := valid
	retrying.Status = DeliveryRetrying
	retrying.NextRetryAt = &nextRetry
	if err := retrying.Validate(); err != nil {
		t.Fatalf("Validate(retrying) error = %v", err)
	}
	done := valid
	done.Status = DeliveryDelivered
	done.FinishedAt = &finished
	if err := done.Validate(); err != nil {
		t.Fatalf("Validate(delivered) error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(Delivery) Delivery
	}{
		{"missing id", func(d Delivery) Delivery { d.ID = " "; return d }},
		{"missing request", func(d Delivery) Delivery { d.RequestID = ""; return d }},
		{"invalid status", func(d Delivery) Delivery { d.Status = "sending"; return d }},
		{"non positive attempt", func(d Delivery) Delivery { d.Attempt = 0; return d }},
		{"retrying without next retry", func(d Delivery) Delivery { d.Status = DeliveryRetrying; return d }},
		{"terminal without finished", func(d Delivery) Delivery { d.Status = DeliveryFailed; return d }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(valid).Validate(); err == nil {
				t.Fatal("Validate() must fail")
			}
		})
	}
}

// TestDeliveryTransition 迁移：非法目标 / 终态吸收 / 时间戳自动回填。
func TestDeliveryTransition(t *testing.T) {
	d, err := NewDelivery("d-1", "r-1", "t-1", ChannelSms, deliveryNow)
	if err != nil {
		t.Fatalf("NewDelivery() error = %v", err)
	}

	if _, err := d.Transition("sending", deliveryNow); err == nil {
		t.Fatal("Transition(sending) must fail on undefined status")
	}
	var invalid *InvalidDeliveryTransitionError
	if _, err := d.Transition(DeliveryDelivered, deliveryNow); !errors.As(err, &invalid) {
		t.Fatalf("Transition(pending->delivered) error = %v, want InvalidDeliveryTransitionError", err)
	}

	later := deliveryNow.Add(time.Minute)
	retrying, err := d.Transition(DeliveryRetrying, later)
	if err != nil {
		t.Fatalf("Transition(retrying) error = %v", err)
	}
	if retrying.NextRetryAt == nil || !retrying.NextRetryAt.Equal(later) {
		t.Fatalf("retrying must set NextRetryAt = %v", retrying.NextRetryAt)
	}
	if !retrying.UpdatedAt.Equal(later) {
		t.Fatalf("Transition() UpdatedAt = %v", retrying.UpdatedAt)
	}

	failed, err := retrying.Transition(DeliveryFailed, later)
	if err != nil {
		t.Fatalf("Transition(failed) error = %v", err)
	}
	if failed.FinishedAt == nil || !failed.FinishedAt.Equal(later) {
		t.Fatalf("terminal must set FinishedAt = %v", failed.FinishedAt)
	}
	if failed.NextRetryAt != nil {
		t.Fatal("terminal must clear NextRetryAt")
	}

	// 终态吸收：任何迁移都必须拒绝，且返回零值（避免调用方误用半成品）。
	if absorbed, err := failed.Transition(DeliveryRetrying, later); !errors.Is(err, ErrDeliveryTerminal) || absorbed.ID != "" {
		t.Fatalf("Transition from terminal = %+v, %v", absorbed, err)
	}

	accepted, err := d.Transition(DeliveryAccepted, later)
	if err != nil {
		t.Fatalf("Transition(accepted) error = %v", err)
	}
	delivered, err := accepted.Transition(DeliveryDelivered, later.Add(time.Minute))
	if err != nil {
		t.Fatalf("Transition(delivered) error = %v", err)
	}
	if delivered.Status != DeliveryDelivered || delivered.FinishedAt == nil {
		t.Fatalf("accepted->delivered = %+v", delivered)
	}
}

// TestDeliveryWithFailure 失败字段：长度约束、截断与去空白，且不改状态。
func TestDeliveryWithFailure(t *testing.T) {
	d, err := NewDelivery("d-1", "r-1", "t-1", ChannelSms, deliveryNow)
	if err != nil {
		t.Fatalf("NewDelivery() error = %v", err)
	}

	failed, err := d.WithFailure(" rate_limited ", " 429 ", "provider busy")
	if err != nil {
		t.Fatalf("WithFailure() error = %v", err)
	}
	if failed.SemanticCode != "rate_limited" || failed.ProviderCode != "429" {
		t.Fatalf("WithFailure() must trim codes: %+v", failed)
	}
	if failed.Status != DeliveryPending {
		t.Fatal("WithFailure() must not change status")
	}
	if d.SemanticCode != "" {
		t.Fatal("WithFailure() must return a copy")
	}

	if _, err := d.WithFailure(strings.Repeat("s", MaxSemanticCodeLength+1), "", ""); !errors.Is(err, ErrSemanticCodeTooLong) {
		t.Fatalf("long semantic code error = %v", err)
	}
	if _, err := d.WithFailure("", strings.Repeat("p", MaxProviderCodeLength+1), ""); !errors.Is(err, ErrProviderCodeTooLong) {
		t.Fatalf("long provider code error = %v", err)
	}

	long := strings.Repeat("x", MaxDeliveryErrorLength+10)
	truncated, err := d.WithFailure("timeout", "ETIMEDOUT", long)
	if err != nil {
		t.Fatalf("WithFailure(long summary) error = %v", err)
	}
	if len([]rune(truncated.LastError)) != MaxDeliveryErrorLength {
		t.Fatalf("summary must be truncated to %d runes, got %d", MaxDeliveryErrorLength, len([]rune(truncated.LastError)))
	}
}

// TestDeliveryWithProvider 供应商记录：必填、限长、去空白。
func TestDeliveryWithProvider(t *testing.T) {
	d, err := NewDelivery("d-1", "r-1", "t-1", ChannelEmail, deliveryNow)
	if err != nil {
		t.Fatalf("NewDelivery() error = %v", err)
	}

	withProvider, err := d.WithProvider(" smtp ")
	if err != nil {
		t.Fatalf("WithProvider() error = %v", err)
	}
	if withProvider.Provider != "smtp" {
		t.Fatalf("WithProvider() = %q, want trimmed", withProvider.Provider)
	}
	if _, err := d.WithProvider("  "); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("empty provider error = %v", err)
	}
	if _, err := d.WithProvider(strings.Repeat("p", MaxProviderLength+1)); !errors.Is(err, ErrProviderTooLong) {
		t.Fatalf("long provider error = %v", err)
	}
}

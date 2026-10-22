package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewBreakglassGrantAdmin(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	grant, err := NewBreakglassGrant(NewBreakglassGrantParams{
		Channel:     ChannelAdmin,
		SubjectType: SubjectStaff,
		SubjectID:   "staff-1",
		Duration:    30 * time.Minute,
		Now:         now,
	})
	if err != nil {
		t.Fatalf("new grant: %v", err)
	}
	if want := now.Add(30 * time.Minute); !grant.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%s want %s", grant.ExpiresAt, want)
	}
	expiry := grant.BindingExpiry()
	if expiry == nil || !expiry.Equal(grant.ExpiresAt) {
		t.Fatalf("binding expiry=%v", expiry)
	}
	// 返回的指针必须独立：调用方修改不得影响值对象。
	*expiry = now
	if !grant.ExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Fatal("binding expiry must be a copy")
	}
}

func TestNewBreakglassGrantBiz(t *testing.T) {
	grant, err := NewBreakglassGrant(NewBreakglassGrantParams{
		Channel:     ChannelBiz,
		SubjectType: SubjectMerchantStaff,
		SubjectID:   "staff-9",
		MerchantID:  "m-1",
		Duration:    MaxBreakglassDuration,
	})
	if err != nil {
		t.Fatalf("new grant: %v", err)
	}
	if grant.MerchantID != "m-1" {
		t.Fatalf("grant=%+v", grant)
	}
}

func TestNewBreakglassGrantRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		args NewBreakglassGrantParams
		want error
	}{
		{
			name: "over max duration",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectStaff, SubjectID: "s-1",
				Duration: MaxBreakglassDuration + time.Second,
			},
			want: ErrBreakglassRequiresExpiry,
		},
		{
			name: "zero duration",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectStaff, SubjectID: "s-1",
			},
			want: ErrBreakglassRequiresExpiry,
		},
		{
			name: "negative duration",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectStaff, SubjectID: "s-1",
				Duration: -time.Minute,
			},
			want: ErrBreakglassRequiresExpiry,
		},
		{
			name: "admin with merchant",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectStaff, SubjectID: "s-1",
				MerchantID: "m-1", Duration: time.Minute,
			},
			want: ErrInvalidBinding,
		},
		{
			name: "admin wrong subject type",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectMerchantStaff, SubjectID: "s-1",
				Duration: time.Minute,
			},
			want: ErrInvalidSubjectType,
		},
		{
			name: "biz without merchant",
			args: NewBreakglassGrantParams{
				Channel: ChannelBiz, SubjectType: SubjectMerchantStaff, SubjectID: "s-1",
				Duration: time.Minute,
			},
			want: ErrInvalidBinding,
		},
		{
			name: "empty subject",
			args: NewBreakglassGrantParams{
				Channel: ChannelAdmin, SubjectType: SubjectStaff, SubjectID: "  ",
				Duration: time.Minute,
			},
			want: ErrEmptyInput,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewBreakglassGrant(tc.args)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

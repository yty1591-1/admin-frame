package timeutil_test

import (
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/timeutil"
)

func TestLocation(t *testing.T) {
	if loc, err := timeutil.Location(""); err != nil || loc != time.UTC {
		t.Fatalf("loc=%v err=%v", loc, err)
	}
	if loc, err := timeutil.Location("UTC"); err != nil || loc != time.UTC {
		t.Fatalf("loc=%v err=%v", loc, err)
	}
	if loc, err := timeutil.Location("Local"); err != nil || loc != time.Local {
		t.Fatalf("loc=%v err=%v", loc, err)
	}
	loc, err := timeutil.Location("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if loc.String() != "Asia/Shanghai" {
		t.Fatalf("loc=%s", loc)
	}
}

func TestLocationCached(t *testing.T) {
	a, err := timeutil.Location("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	b, err := timeutil.Location("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("expected cached location pointer")
	}
}

func TestLocationInvalid(t *testing.T) {
	if _, err := timeutil.Location("Not/AZone"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMustLocation(t *testing.T) {
	if got := timeutil.MustLocation("UTC"); got != time.UTC {
		t.Fatalf("loc=%v", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = timeutil.MustLocation("Not/AZone")
}

func TestNormalizeLocation(t *testing.T) {
	if timeutil.NormalizeLocation(nil) != time.UTC {
		t.Fatal("nil should normalize to UTC")
	}
	loc := time.FixedZone("X", 8*3600)
	if timeutil.NormalizeLocation(loc) != loc {
		t.Fatal("non-nil should be preserved")
	}
}

func TestStartOfDayAndRange(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 10, 20, 15, 4, 5, 123, loc)
	start := timeutil.StartOfDay(at, loc)
	want := time.Date(2026, 10, 20, 0, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start=%s want=%s", start, want)
	}
	s, e := timeutil.DayRange(at, loc)
	if !s.Equal(want) || !e.Equal(want.AddDate(0, 0, 1)) {
		t.Fatalf("range=[%s,%s)", s, e)
	}
}

func TestDayRangeAcrossDST(t *testing.T) {
	loc, err := timeutil.Location("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-03-08 当地进入夏令时，当天只有 23 小时。
	at := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	s, e := timeutil.DayRange(at, loc)
	if got := e.Sub(s); got != 23*time.Hour {
		t.Fatalf("spring forward length=%s", got)
	}
	// 2026-11-01 退出夏令时，当天 25 小时。
	at = time.Date(2026, 11, 1, 12, 0, 0, 0, loc)
	s, e = timeutil.DayRange(at, loc)
	if got := e.Sub(s); got != 25*time.Hour {
		t.Fatalf("fall back length=%s", got)
	}
}

func TestStartOfWeek(t *testing.T) {
	loc := time.UTC
	// 2026-10-20 是周二，ISO 周起点为 2026-10-19 周一。
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, loc)
	start := timeutil.StartOfWeek(at, loc)
	want := time.Date(2026, 10, 19, 0, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start=%s want=%s", start, want)
	}
	if start.Weekday() != time.Monday {
		t.Fatalf("weekday=%s", start.Weekday())
	}
	// 周日应归入前一个周一开始的 ISO 周。
	sunday := time.Date(2026, 10, 25, 23, 0, 0, 0, loc)
	if got := timeutil.StartOfWeek(sunday, loc); !got.Equal(want) {
		t.Fatalf("sunday start=%s want=%s", got, want)
	}
	s, e := timeutil.WeekRange(at, loc)
	if e.Sub(s) != 7*24*time.Hour {
		t.Fatalf("range length=%s", e.Sub(s))
	}
}

func TestMonthAndYear(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, loc)
	if got, want := timeutil.StartOfMonth(at, loc), time.Date(2026, 10, 1, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("month start=%s want=%s", got, want)
	}
	s, e := timeutil.MonthRange(at, loc)
	if !s.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("month start=%s", s)
	}
	if !e.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("month end=%s", e)
	}
	if got, want := timeutil.StartOfYear(at, loc), time.Date(2026, 1, 1, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("year start=%s want=%s", got, want)
	}
	s, e = timeutil.YearRange(at, loc)
	if !s.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, loc)) || !e.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("year range=[%s, %s)", s, e)
	}
}

func TestDaysInMonth(t *testing.T) {
	tests := []struct {
		name  string
		year  int
		month time.Month
		want  int
	}{
		{name: "jan", year: 2026, month: time.January, want: 31},
		{name: "feb common", year: 2026, month: time.February, want: 28},
		{name: "feb leap", year: 2028, month: time.February, want: 29},
		{name: "apr", year: 2026, month: time.April, want: 30},
		{name: "december", year: 2026, month: time.December, want: 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timeutil.DaysInMonth(tt.year, tt.month); got != tt.want {
				t.Fatalf("got=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestSameDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	a := time.Date(2026, 10, 20, 0, 0, 1, 0, loc)
	b := time.Date(2026, 10, 20, 23, 59, 59, 0, loc)
	if !timeutil.SameDay(a, b, loc) {
		t.Fatal("expected same day")
	}
	c := time.Date(2026, 10, 21, 0, 0, 0, 0, loc)
	if timeutil.SameDay(a, c, loc) {
		t.Fatal("expected different day")
	}
}

func TestBusinessCalendarDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	cal, err := timeutil.NewBusinessCalendar(loc, 4*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{
			name: "before cutoff belongs to previous day",
			at:   time.Date(2026, 10, 20, 2, 0, 0, 0, loc),
			want: time.Date(2026, 10, 19, 0, 0, 0, 0, loc),
		},
		{
			name: "at cutoff starts new day",
			at:   time.Date(2026, 10, 20, 4, 0, 0, 0, loc),
			want: time.Date(2026, 10, 20, 0, 0, 0, 0, loc),
		},
		{
			name: "late night same day",
			at:   time.Date(2026, 10, 20, 23, 59, 59, 0, loc),
			want: time.Date(2026, 10, 20, 0, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cal.Day(tt.at); !got.Equal(tt.want) {
				t.Fatalf("day=%s want=%s", got, tt.want)
			}
		})
	}
}

func TestBusinessCalendarRange(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	cal := timeutil.MustBusinessCalendar(loc, 4*time.Hour)
	at := time.Date(2026, 10, 20, 2, 0, 0, 0, loc)
	s, e := cal.Range(at)
	wantStart := time.Date(2026, 10, 19, 4, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 10, 20, 4, 0, 0, 0, loc)
	if !s.Equal(wantStart) || !e.Equal(wantEnd) {
		t.Fatalf("range=[%s,%s)", s, e)
	}
	if !cal.Contains(cal.Day(at), at) {
		t.Fatal("range should contain at")
	}
	if cal.Contains(cal.Day(at), e) {
		t.Fatal("range end must be exclusive")
	}
}

func TestBusinessCalendarInvalid(t *testing.T) {
	for _, cutoff := range []time.Duration{-time.Second, 24 * time.Hour, 25 * time.Hour} {
		if _, err := timeutil.NewBusinessCalendar(time.UTC, cutoff); !errors.Is(err, timeutil.ErrInvalidCutoff) {
			t.Fatalf("cutoff=%s err=%v", cutoff, err)
		}
	}
}

func TestBusinessCalendarNilLocation(t *testing.T) {
	cal, err := timeutil.NewBusinessCalendar(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cal.Location() != time.UTC {
		t.Fatalf("loc=%v", cal.Location())
	}
	if cal.Cutoff() != 0 {
		t.Fatalf("cutoff=%s", cal.Cutoff())
	}
}

func TestBusinessCalendarRangeAcrossDST(t *testing.T) {
	loc, err := timeutil.Location("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cal := timeutil.MustBusinessCalendar(loc, 0)

	spring := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	s, e := cal.Range(spring)
	if got := e.Sub(s); got != 23*time.Hour {
		t.Fatalf("spring forward=%s", got)
	}
	if s.Hour() != 0 || s.Minute() != 0 {
		t.Fatalf("spring start=%s", s)
	}

	fall := time.Date(2026, 11, 1, 12, 0, 0, 0, loc)
	s, e = cal.Range(fall)
	if got := e.Sub(s); got != 25*time.Hour {
		t.Fatalf("fall back=%s", got)
	}
	if !cal.Contains(cal.Day(fall), fall) {
		t.Fatal("fall range should contain at")
	}
}

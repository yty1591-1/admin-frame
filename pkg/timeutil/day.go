package timeutil

import "time"

// StartOfDay 返回 t 所在本地日历日的 00:00。
func StartOfDay(t time.Time, loc *time.Location) time.Time {
	t = InLocation(t, loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// DayRange 返回 t 所在本地日历日的左闭右开区间。
//
// 跨夏令时切换时区间长度可能为 23h 或 25h，属预期行为。
func DayRange(t time.Time, loc *time.Location) (start, end time.Time) {
	start = StartOfDay(t, loc)
	return start, start.AddDate(0, 0, 1)
}

// StartOfWeek 返回 t 所在 ISO 周（周一为第一天）的 00:00。
func StartOfWeek(t time.Time, loc *time.Location) time.Time {
	day := StartOfDay(t, loc)
	// time.Weekday：Sunday=0；转换为 ISO 的 Monday=0。
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

// WeekRange 返回 t 所在 ISO 周的左闭右开区间。
func WeekRange(t time.Time, loc *time.Location) (start, end time.Time) {
	start = StartOfWeek(t, loc)
	return start, start.AddDate(0, 0, 7)
}

// StartOfMonth 返回 t 所在本地月份第一天的 00:00。
func StartOfMonth(t time.Time, loc *time.Location) time.Time {
	t = InLocation(t, loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// MonthRange 返回 t 所在本地月份的左闭右开区间。
func MonthRange(t time.Time, loc *time.Location) (start, end time.Time) {
	start = StartOfMonth(t, loc)
	return start, start.AddDate(0, 1, 0)
}

// StartOfYear 返回 t 所在本地年份第一天的 00:00。
func StartOfYear(t time.Time, loc *time.Location) time.Time {
	t = InLocation(t, loc)
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
}

// YearRange 返回 t 所在本地年份的左闭右开区间。
func YearRange(t time.Time, loc *time.Location) (start, end time.Time) {
	start = StartOfYear(t, loc)
	return start, start.AddDate(1, 0, 0)
}

// DaysInMonth 返回指定年月的天数；month 超出 1..12 时按 time.Date 归一化。
func DaysInMonth(year int, month time.Month) int {
	// 下个月第 0 天即本月最后一天。
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// SameDay 判断 a、b 是否落在 loc 下的同一日历日。
func SameDay(a, b time.Time, loc *time.Location) bool {
	loc = NormalizeLocation(loc)
	aa, bb := a.In(loc), b.In(loc)
	ay, am, ad := aa.Date()
	by, bm, bd := bb.Date()
	return ay == by && am == bm && ad == bd
}

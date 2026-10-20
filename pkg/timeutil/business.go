package timeutil

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidCutoff 表示业务日切偏移不合法。
var ErrInvalidCutoff = errors.New("timeutil: invalid business day cutoff")

// BusinessCalendar 定义「业务日切」：本地时间 cutoff 之后才开始新的业务日。
//
// 语义：业务日 D 覆盖 [D 00:00 + cutoff, D+1 00:00 + cutoff)。
// cutoff 为 0 时等价于自然日；例如 cutoff=4h 时，02:00 属于前一业务日。
// 日切按本地墙钟时间解释，跨夏令时仍落在本地 cutoff 时刻。
// BusinessCalendar 不可变，可安全并发使用。
type BusinessCalendar struct {
	loc    *time.Location
	cutoff time.Duration
}

// NewBusinessCalendar 创建业务日历。
//
// loc 为 nil 时按 UTC；cutoff 必须落在 [0, 24h) 内，否则返回 ErrInvalidCutoff。
func NewBusinessCalendar(loc *time.Location, cutoff time.Duration) (*BusinessCalendar, error) {
	if cutoff < 0 || cutoff >= 24*time.Hour {
		return nil, fmt.Errorf("%w: %s must be in [0,24h)", ErrInvalidCutoff, cutoff)
	}
	return &BusinessCalendar{loc: NormalizeLocation(loc), cutoff: cutoff}, nil
}

// MustBusinessCalendar 同 NewBusinessCalendar，参数非法时 panic。
func MustBusinessCalendar(loc *time.Location, cutoff time.Duration) *BusinessCalendar {
	cal, err := NewBusinessCalendar(loc, cutoff)
	if err != nil {
		panic(err)
	}
	return cal
}

// Location 返回日历使用的时区。
func (c *BusinessCalendar) Location() *time.Location { return c.loc }

// Cutoff 返回业务日切偏移。
func (c *BusinessCalendar) Cutoff() time.Duration { return c.cutoff }

// Day 返回 t 所属业务日的标签（本地 00:00）。
//
// 该值用于「哪一天」的比较与数据库日期字段，不是业务日区间的起点；
// 区间请用 Range。
func (c *BusinessCalendar) Day(t time.Time) time.Time {
	local := t.In(c.loc)
	year, month, day := local.Date()
	if wallClock(local) < c.cutoff {
		// time.Date 会自动归一化跨月/跨年。
		year, month, day = time.Date(year, month, day-1, 0, 0, 0, 0, c.loc).Date()
	}
	return time.Date(year, month, day, 0, 0, 0, 0, c.loc)
}

// Range 返回 t 所属业务日的左闭右开区间。
func (c *BusinessCalendar) Range(t time.Time) (start, end time.Time) {
	day := c.Day(t)
	return c.start(day), c.start(day.AddDate(0, 0, 1))
}

// Contains 判断 t 是否落在业务日 day 的区间内。
//
// day 只需日期部分正确（通常来自 Day）；其时分秒会被忽略。
func (c *BusinessCalendar) Contains(day, t time.Time) bool {
	local := day.In(c.loc)
	label := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, c.loc)
	start := c.start(label)
	end := c.start(label.AddDate(0, 0, 1))
	return !t.Before(start) && t.Before(end)
}

// start 返回标签日为 day 的业务日在本地 cutoff 时刻的瞬时。
func (c *BusinessCalendar) start(day time.Time) time.Time {
	year, month, d := day.Date()
	hour := int(c.cutoff / time.Hour)
	remainder := c.cutoff % time.Hour
	minute := int(remainder / time.Minute)
	remainder %= time.Minute
	second := int(remainder / time.Second)
	nanosecond := int(remainder % time.Second)
	return time.Date(year, month, d, hour, minute, second, nanosecond, c.loc)
}

// wallClock 返回 t 在当天的本地墙钟时长。
func wallClock(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour +
		time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second +
		time.Duration(t.Nanosecond())
}

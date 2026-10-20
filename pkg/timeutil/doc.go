// Package timeutil 提供时区、日/周/月边界与业务日切工具。
//
// 设计边界：
//   - 不得依赖 internal/；不包含节假日、交易日历等业务规则
//   - 所有区间均为左闭右开 [start, end)，避免跨日/跨月边界重复或遗漏
//   - 周以 ISO 8601 为准：周一为第一天
//   - 传入 nil *time.Location 时按 UTC 处理；业务时区应由调用方显式注入
//   - 业务日切只支持固定「本地时间偏移」，不支持每日变化的日切点
//
// 典型用法：
//
//	loc, err := timeutil.Location("Asia/Shanghai")
//	if err != nil {
//		return err
//	}
//	start, end := timeutil.DayRange(time.Now(), loc)
//
//	cal, err := timeutil.NewBusinessCalendar(loc, 4*time.Hour)
//	day := cal.Day(time.Now()) // 凌晨 4 点前仍算前一业务日
package timeutil

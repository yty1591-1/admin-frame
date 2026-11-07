package observability

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/pkg/db"
)

// RegisterDBMetrics 注册连接池指标（ENT-06 db.pool.exhaustion 告警的数据源）。
//
// database 为 nil（db.driver=memory）或 reg 为 nil 时直接返回，便于本地/单测复用。
// 指标名不带 namespace，与 pkg/metrics 的其它指标保持一致，并继承 service/env 常量标签。
func RegisterDBMetrics(reg prometheus.Registerer, database *db.DB) error {
	if reg == nil || database == nil {
		return nil
	}
	stats := database.Stats

	collectors := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_open_connections",
			Help: "当前打开的数据库连接数（含空闲与使用中）。",
		}, func() float64 { return float64(stats().OpenConnections) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_in_use_connections",
			Help: "正在使用中的数据库连接数。",
		}, func() float64 { return float64(stats().InUse) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_idle_connections",
			Help: "空闲数据库连接数。",
		}, func() float64 { return float64(stats().Idle) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_max_open_connections",
			Help: "连接池上限（db.max_open_conns 派生）。",
		}, func() float64 { return float64(stats().MaxOpenConnections) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "db_pool_wait_total",
			Help: "因连接池耗尽而等待的累计次数。",
		}, func() float64 { return float64(stats().WaitCount) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "db_pool_wait_duration_seconds_total",
			Help: "等待数据库连接的累计耗时（秒）。",
		}, func() float64 { return stats().WaitDuration.Seconds() }),
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return fmt.Errorf("db metrics: register: %w", err)
		}
	}
	return nil
}

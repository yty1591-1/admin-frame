// Package observability 提供平台级的日志与指标装配：按配置构造结构化 logger、
// 注册数据库连接池指标（OBS-05 / ENT-06 告警数据源）。
//
// 边界：本包只做构造与注册，不含业务埋点；指标与日志原语在 pkg/{metrics,logger}，
// 追踪与请求级指标由 internal/platform/httpserver 的管线负责。
package observability

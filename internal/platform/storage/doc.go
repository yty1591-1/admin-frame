// Package storage 是对象存储的装配层：按配置构造 Store、管理同进程多桶集合
// 与启动期的桶生命周期（M3 / M5）。
//
// 边界：驱动实现（local / s3 / minio / oss / cos / obs）在 pkg/storage；
// 本包只做配置映射、桶选择与健康检查接线，不含业务语义。
package storage

// Package config 定义平台段配置 schema、默认值、归一化与启动期校验：
// HTTP、日志、鉴权、数据库、Redis、对象存储、限流、幂等、异步、追踪与密钥解析。
//
// 边界：域配置（identity / iam / audit / tenant）由各自 internal/<domain>/config
// 自持，本包不感知；组合装配与聚合校验由 cmd/internal/appconfig 完成。
// 密钥不落配置文件，经 pkg/config 的环境变量绑定或密钥提供者链注入。
package config

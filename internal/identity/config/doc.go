// Package config 定义身份域配置：撤销存储、哈希 / MFA / SAML 密钥引用、令牌与限额参数。
//
// 边界：本包自持结构体、默认值与校验（ARCHITECTURE §十三 域配置自持）；平台段配置在
// internal/platform/config，两者由 cmd/internal/appconfig 组合加载与聚合校验。
// 密钥不落配置文件，仅存引用名，实际取值经密钥提供者链注入。
package config

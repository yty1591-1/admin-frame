package config

import (
	auditconfig "github.com/admin-frame/admin-frame/internal/audit/config"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	tenantconfig "github.com/admin-frame/admin-frame/internal/tenant/config"
)

// 域配置类型别名。
//
// 定义已下沉到各业务域（internal/{identity,iam,audit,tenant}/config）；
// 平台层只保留聚合结构与「加载 + 绑定 + 编排校验」，通过别名维持既有调用形态。
type (
	IdentityConfig                 = identityconfig.IdentityConfig
	IdentityConsumerConfig         = identityconfig.IdentityConsumerConfig
	IdentityHashKeyConfig          = identityconfig.IdentityHashKeyConfig
	IdentityLockoutConfig          = identityconfig.IdentityLockoutConfig
	IdentityLoginRateLimitConfig   = identityconfig.IdentityLoginRateLimitConfig
	IdentityMFAConfig              = identityconfig.IdentityMFAConfig
	IdentityMFAKeyConfig           = identityconfig.IdentityMFAKeyConfig
	IdentityOAuthConfig            = identityconfig.IdentityOAuthConfig
	IdentityOAuthProviderConfig    = identityconfig.IdentityOAuthProviderConfig
	IdentityPasswordConfig         = identityconfig.IdentityPasswordConfig
	IdentityPasswordTenantOverride = identityconfig.IdentityPasswordTenantOverride
	IdentityRetentionConfig        = identityconfig.IdentityRetentionConfig
	IdentityRevocationConfig       = identityconfig.IdentityRevocationConfig
	IdentitySAMLConfig             = identityconfig.IdentitySAMLConfig
	IdentitySAMLProviderConfig     = identityconfig.IdentitySAMLProviderConfig
	IdentitySMSConfig              = identityconfig.IdentitySMSConfig
	IdentitySessionConfig          = identityconfig.IdentitySessionConfig
	IdentitySubjectConfig          = identityconfig.IdentitySubjectConfig

	IAMApprovalConfig      = iamconfig.IAMApprovalConfig
	IAMBreakglassConfig    = iamconfig.IAMBreakglassConfig
	IAMCacheConfig         = iamconfig.IAMCacheConfig
	IAMCertificationConfig = iamconfig.IAMCertificationConfig
	IAMConfig              = iamconfig.IAMConfig
	IAMConstraintsConfig   = iamconfig.IAMConstraintsConfig
	IAMLimitsConfig        = iamconfig.IAMLimitsConfig
	IAMSecurityConfig      = iamconfig.IAMSecurityConfig

	AuditConfig          = auditconfig.AuditConfig
	AuditExportConfig    = auditconfig.AuditExportConfig
	AuditQueryConfig     = auditconfig.AuditQueryConfig
	AuditRetentionConfig = auditconfig.AuditRetentionConfig

	TenantConfig          = tenantconfig.TenantConfig
	TenantRetentionConfig = tenantconfig.TenantRetentionConfig
)

// 域配置常量别名。
const (
	IdentityRevocationDriverRedis = identityconfig.IdentityRevocationDriverRedis
	IdentitySMSDriverAliyun       = identityconfig.IdentitySMSDriverAliyun
	IdentitySMSDriverMemory       = identityconfig.IdentitySMSDriverMemory

	IAMModeOff                = iamconfig.IAMModeOff
	IAMModeShadow             = iamconfig.IAMModeShadow
	IAMModeEnforce            = iamconfig.IAMModeEnforce
	IAMCacheDriverMemory      = iamconfig.IAMCacheDriverMemory
	IAMCacheDriverRedis       = iamconfig.IAMCacheDriverRedis
	IAMConstraintsModeOff     = iamconfig.IAMConstraintsModeOff
	IAMConstraintsModeShadow  = iamconfig.IAMConstraintsModeShadow
	IAMConstraintsModeEnforce = iamconfig.IAMConstraintsModeEnforce

	AuditStoreAuto        = auditconfig.AuditStoreAuto
	AuditStoreDB          = auditconfig.AuditStoreDB
	AuditStoreLog         = auditconfig.AuditStoreLog
	AuditMinRetentionDays = auditconfig.AuditMinRetentionDays
	AuditMaxRetentionDays = auditconfig.AuditMaxRetentionDays
)

// Package platform 是进程级运行时装配层。
//
// 拆分后的能力位于子包；本文件保留一版导出兼容层（类型别名 + 函数转发），
// 使 cmd/* 与既有测试无需随拆分改 import。
package platform

import (
	"context"
	"fmt"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform/async"
	"github.com/admin-frame/admin-frame/internal/platform/authn"
	"github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/platform/observability"
	"github.com/admin-frame/admin-frame/internal/platform/storage"
)

// 配置类型别名（定义位于 internal/platform/config）。
//
// 域配置（Identity/IAM/Audit/Tenant）已下沉到各自 internal/<domain>/config 包，
// 平台根不再转发，避免平台与应用层形成反向依赖。
type (
	Config              = config.Config
	AsyncBrokerConfig   = config.AsyncBrokerConfig
	AsyncConfig         = config.AsyncConfig
	AsyncConsumerConfig = config.AsyncConsumerConfig
	AsyncOutboxConfig   = config.AsyncOutboxConfig
	AuthConfig          = config.AuthConfig
	CORSConfig          = config.CORSConfig
	DBConfig            = config.DBConfig
	HTTPConfig          = config.HTTPConfig
	IDConfig            = config.IDConfig
	IdempotencyConfig   = config.IdempotencyConfig
	LogConfig           = config.LogConfig
	RateLimitConfig     = config.RateLimitConfig
	RedisConfig         = config.RedisConfig
	SecretsConfig       = config.SecretsConfig
	SecretsEnvConfig    = config.SecretsEnvConfig
	SecretsFileConfig   = config.SecretsFileConfig
	SecretsVaultConfig  = config.SecretsVaultConfig
	TracingConfig       = config.TracingConfig
	WorkerConfig        = config.WorkerConfig
)

// 存储与 HTTP 服务类型别名（定义位于对应子包）。
type (
	StorageConfig     = storage.StorageConfig
	BucketConfig      = storage.BucketConfig
	StorageSet        = storage.StorageSet
	BuildInfo         = httpserver.BuildInfo
	PipelineConfig    = httpserver.PipelineConfig
	PipelineDeps      = httpserver.PipelineDeps
	AsyncRuntime      = async.AsyncRuntime
	AsyncMetrics      = async.AsyncMetrics
	BrokerObserver    = async.BrokerObserver
	NATSBrokerOptions = async.NATSBrokerOptions
)

// 配置常量转发。
const (
	AsyncBackoffExponential            = config.AsyncBackoffExponential
	AsyncBackoffFixed                  = config.AsyncBackoffFixed
	AsyncDriverDB                      = config.AsyncDriverDB
	AsyncDriverNATS                    = config.AsyncDriverNATS
	AuthModeDemo                       = config.AuthModeDemo
	AuthModeJWT                        = config.AuthModeJWT
	IdempotencyDriverMemory            = config.IdempotencyDriverMemory
	IdempotencyDriverRedis             = config.IdempotencyDriverRedis
	IDSourceConfig                     = config.IDSourceConfig
	IDSourceEnv                        = config.IDSourceEnv
	IDSourceStatefulSet                = config.IDSourceStatefulSet
	RateLimitDriverMemory              = config.RateLimitDriverMemory
	RateLimitDriverRedis               = config.RateLimitDriverRedis
	RateLimitFailureClosed             = config.RateLimitFailureClosed
	RateLimitFailureOpen               = config.RateLimitFailureOpen
	RateLimitKeyChannel                = config.RateLimitKeyChannel
	RateLimitKeyChannelTenantUser      = config.RateLimitKeyChannelTenantUser
	RateLimitKeyChannelUser            = config.RateLimitKeyChannelUser
	RateLimitKeyClient                 = config.RateLimitKeyClient
	SecretAuthJWTSecret                = config.SecretAuthJWTSecret
	SecretAuthJWTSecrets               = config.SecretAuthJWTSecrets
	SecretDBDSN                        = config.SecretDBDSN
	SecretIdentityHashKey              = config.SecretIdentityHashKey
	SecretIdentityMFASecretKey         = config.SecretIdentityMFASecretKey
	SecretIdentitySAMLSPPrivateKey     = config.SecretIdentitySAMLSPPrivateKey
	SecretIdentitySAMLSPPrivateKeyNext = config.SecretIdentitySAMLSPPrivateKeyNext
	SecretProviderEnv                  = config.SecretProviderEnv
	SecretProviderFile                 = config.SecretProviderFile
	SecretProviderVault                = config.SecretProviderVault
	SecretRedisPassword                = config.SecretRedisPassword
	SecretStorageS3AccessKey           = config.SecretStorageS3AccessKey
	SecretStorageS3SecretKey           = config.SecretStorageS3SecretKey
)

// maxNodeID 与 pkg/id 的雪花节点号上限一致（顶层 ID 装配使用）。
const maxNodeID = config.MaxNodeID

// 配置加载与存储转发。
var (
	LoadConfig      = config.LoadConfig
	LoadDBConfig    = config.LoadDBConfig
	NewConfigLoader = config.NewConfigLoader
	IsProdEnv       = config.IsProdEnv

	OpenStorage         = storage.OpenStorage
	OpenStorageSet      = storage.OpenStorageSet
	ValidateStorage     = storage.ValidateStorage
	MaybeApplyLifecycle = storage.MaybeApplyLifecycle

	NewLogger         = observability.NewLogger
	RegisterDBMetrics = observability.RegisterDBMetrics

	Chain          = httpserver.Chain
	Recover        = httpserver.Recover
	RequestID      = httpserver.RequestID
	AccessLog      = httpserver.AccessLog
	NewPipeline    = httpserver.NewPipeline
	ListenAndServe = httpserver.ListenAndServe
	MountHealth    = httpserver.MountHealth
	MountVersion   = httpserver.MountVersion
	MountMetrics   = httpserver.MountMetrics

	NewAuthMiddleware = authn.NewAuthMiddleware

	NewAsyncStore   = async.NewAsyncStore
	NewAsyncDeduper = async.NewAsyncDeduper
	NewAsyncMetrics = async.NewAsyncMetrics
	NewNATSBroker   = async.NewNATSBroker
)

// WithTenantGuard 在鉴权之后校验租户状态（ENT-09）。
//
// 兼容包装：旧签名接收命名接口，现改为接收实现的方法值（tenantapp.Guard.Verify）。
// 业务语义端口已下沉到 internal/tenant/app，平台只保留通用中间件装配点。
func WithTenantGuard(mw func(http.Handler) http.Handler, verify func(ctx context.Context, merchantID string) (bool, error), requireMerchant bool) func(http.Handler) http.Handler {
	return httpserver.AfterAuth(mw, httpserver.TenantGuardMiddleware(verify, requireMerchant))
}

// WithQuotaGuard 在租户状态守卫之后执行 API 配额（ENT-14）。
func WithQuotaGuard(mw func(http.Handler) http.Handler, allow func(ctx context.Context, tenantID string) (int, error)) func(http.Handler) http.Handler {
	return httpserver.AfterAuth(mw, httpserver.QuotaGuardMiddleware(allow))
}

// NewAsyncRuntime 把根包 Deps 转译为子包局部依赖，避免 async 反向依赖平台根包。
func NewAsyncRuntime(deps *Deps) (*AsyncRuntime, error) {
	if deps == nil {
		return nil, fmt.Errorf("async runtime: deps is required")
	}
	return async.NewAsyncRuntime(async.RuntimeDeps{
		Config:   deps.Config,
		Logger:   deps.Logger,
		Database: deps.Database,
		Metrics:  deps.Metrics,
	})
}

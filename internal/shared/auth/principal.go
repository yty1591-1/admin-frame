package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Channel 业务端别，与架构文档一致。
type Channel string

const (
	// ChannelAdmin 运营后台（A 端）。
	ChannelAdmin Channel = "admin"
	// ChannelBiz 商家端（B 端）。
	ChannelBiz Channel = "biz"
	// ChannelC 消费者端（C 端）。
	ChannelC Channel = "c"
	// ChannelInternal 内部服务通道（如 config-api）；不对外签发用户令牌。
	ChannelInternal Channel = "internal"
)

// PermissionMiddleware 是权限中间件工厂：给定权限点返回 HTTP 中间件。
//
// 由 cmd 在装配期注入（如 security.Authorizer.Require），使 transport 只依赖
// shared 契约，而不必 import internal/platform（ARCHITECTURE §6.1）。
type PermissionMiddleware func(permission string) func(http.Handler) http.Handler

// token_use 声明取值（docs/identity-authn.md §4.1 / §21.2.3）。
const (
	// TokenUseUser 人工用户令牌；缺省该声明时按用户令牌兼容处理。
	TokenUseUser = "user"
	// TokenUseClient M2M 机器令牌；只按 scope 授权，不查账号会话。
	TokenUseClient = "client"
)

// Principal 认证后的主体上下文。
//
// 请求链路中的 request_id / user_id / merchant_id / channel 原语见 pkg/contextx；
// 鉴权成功后应由 middleware 同时写入 contextx，供非 auth 层只读使用。
type Principal struct {
	Channel    Channel
	SubjectID  string
	MerchantID string
	Roles      []string
	RequestID  string
	// SessionID 当前访问令牌绑定的会话（jti 之外的 sid 声明）；旧令牌为空。
	SessionID string
	// TokenID 当前访问令牌的 jti，用于单会话撤销。
	TokenID string
	// TokenVersion 令牌签发时的账号撤销版本（ver）。
	TokenVersion int64
	// TokenExpiresAt 令牌到期时间，用于计算 jti 黑名单 TTL。
	TokenExpiresAt time.Time
	// CurrentTenantID 当前生效租户（T-22，B 端多租户切换）。
	// 旧令牌没有该声明（为空），此时以 MerchantID 兜底——见 CurrentTenant。
	CurrentTenantID string
	// ClientID M2M 客户端标识；仅 token_use=client 时非空。
	ClientID string
	// Scopes M2M 令牌授予的权限码；仅 token_use=client 时非空。
	//
	// 机器主体的授权只看该集合（精确匹配），不做角色快照（§21.2.3）。
	Scopes []string
	// TokenUse 令牌用途：user（缺省）/ client。
	TokenUse string
}

// IsClient 报告当前主体是否为 M2M 机器主体。
func (p Principal) IsClient() bool {
	return p.TokenUse == TokenUseClient
}

// CurrentTenant 返回请求的生效租户：current_tenant 声明优先，
// 旧令牌（无该声明）回落到签发时绑定的 merchant_id。
//
// 这是 T-22 的「旧令牌行为明确定义」：切换前签发的令牌继续以
// merchant_id 生效到自然过期，不会被切换动作作废。
func (p Principal) CurrentTenant() string {
	if strings.TrimSpace(p.CurrentTenantID) != "" {
		return p.CurrentTenantID
	}
	return p.MerchantID
}

type ctxKey struct{}

// WithPrincipal 将 Principal 写入 context。
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext 读取 Principal；不存在时 ok 为 false。
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// MustFromContext 读取 Principal；缺失时 panic（仅用于已保证鉴权通过的路径）。
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: principal missing from context")
	}
	return p
}

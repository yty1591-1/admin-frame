package notificationruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/platform"
	tenantapp "github.com/admin-frame/admin-frame/internal/tenant/app"
)

// 轻量出口与租户邀请通知适配器（ENT-10 NT-09）。
//
// NewDispatcher 供只需要「事务内写 Outbox」的调用方使用（如 A 端代发、
// 邀请通知），不需要渠道路由与黑名单盐——编排消费在 worker 进程。

// EnvBizBaseURL 是 B 端接受链接的站点基址（邀请邮件 invite_url 变量）。
const EnvBizBaseURL = "ADMIN_FRAME_BIZ_BASE_URL"

// NewDispatcher 构造生产侧出口；依赖 async 基础设施（Outbox Store）。
func NewDispatcher(deps *platform.Deps) (*app.OutboxDispatcher, error) {
	if deps == nil || deps.Async == nil || deps.Async.Store() == nil {
		return nil, fmt.Errorf("notification dispatcher requires async infrastructure (async.enabled=true)")
	}
	return app.NewOutboxDispatcher(deps.Async.Store())
}

// InvitationNotifier 把租户邀请事件适配为 notification.requested 事件
// （实现 tenant/app 的 InvitationNotifier 端口，ENT-09 T-14 ↔ ENT-10 闭环）。
//
// 语义：
//   - 事件与邀请写入同事务（Dispatch 复用 ctx 事务），失败整体回滚；
//   - 幂等键 = notify:tenant.invitation:<邀请ID>:<token 摘要前 8 位>：
//     重发轮换 token 即新键，同一 token 重放被 Outbox 主键幂等吸收；
//   - 收件人引用 = 邮箱 SHA-256 摘要前 32 位（通知表不存明文地址）；
//   - 场景分类 transactional：偏好层不可退订（账号安全动作必须可达）。
type InvitationNotifier struct {
	dispatcher *app.OutboxDispatcher
	// baseURL 接受链接基址；空取 env，仍为空时用本地占位（memory 驱动阶段不出网）。
	baseURL string
}

// NewInvitationNotifier 构造适配器。
func NewInvitationNotifier(dispatcher *app.OutboxDispatcher, baseURL string) *InvitationNotifier {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = os.Getenv(EnvBizBaseURL)
	}
	if strings.TrimSpace(baseURL) == "" {
		// M4 阶段语义兜底：memory 驱动不出网，占位只影响渲染结果不影响投递链路。
		baseURL = "https://biz.local"
	}
	return &InvitationNotifier{dispatcher: dispatcher, baseURL: strings.TrimRight(baseURL, "/")}
}

// NotifyInvitationCreated 实现 tenantapp.InvitationNotifier。
func (n *InvitationNotifier) NotifyInvitationCreated(ctx context.Context,
	event tenantapp.InvitationNotifyEvent) error {
	if n.dispatcher == nil {
		return fmt.Errorf("notification dispatcher is not configured")
	}
	trimmedEmail := strings.TrimSpace(event.Email)
	if trimmedEmail == "" {
		return fmt.Errorf("invitation notify: email is required")
	}
	tokenDigest := sha256.Sum256([]byte(event.Token))
	emailDigest := sha256.Sum256([]byte(trimmedEmail))
	payload := app.RequestPayload{
		SchemaVersion: app.SchemaVersionV1,
		RequestID:     "ntf-" + event.InvitationID + "-" + hex.EncodeToString(tokenDigest[:])[:8],
		TenantID:      event.TenantID,
		Scene:         "tenant.invitation",
		SceneClass:    string(domain.ClassTransactional),
		// 收件人引用取摘要：通知表（90 天保留）不落明文地址。
		RecipientRef: hex.EncodeToString(emailDigest[:])[:32],
		Recipient:    app.Recipient{Email: trimmedEmail},
		Channels:     []string{string(domain.ChannelEmail)},
		TemplateKey:  "tenant.invitation",
		Locale:       "zh-CN",
		Params: map[string]string{
			"tenant_name": event.TenantName,
			"invitee":     trimmedEmail,
			"invite_url":  n.baseURL + "/invite/" + event.Token,
			"expires_at":  event.ExpiresAt.UTC().Format("2006-01-02 15:04"),
		},
		// 幂等键含 token 摘要：重发轮换 token 即新键，同一 token 重放被吸收。
		IdempotencyKey: "notify:tenant.invitation:" + event.InvitationID + ":" + hex.EncodeToString(tokenDigest[:])[:8],
		Priority:       string(domain.PriorityNormal),
		Actor:          "tenant.invitation",
		Reference:      event.InvitationID,
		ExpiresAt:      event.ExpiresAt,
	}
	return n.dispatcher.Dispatch(ctx, payload)
}

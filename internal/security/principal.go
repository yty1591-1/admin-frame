package security

import (
	"context"

	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/contextx"
)

// withPrincipal 同时写入 auth.Principal 与 contextx.Identity。
//
// 两处写入必须成对出现：auth.Principal 供鉴权/授权读取，contextx 供日志、
// 限流键与非 auth 层只读使用（ARCHITECTURE §7.1）。
func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	ctx = auth.WithPrincipal(ctx, p)
	return contextx.WithIdentity(ctx, contextx.Identity{
		RequestID:  p.RequestID,
		UserID:     p.SubjectID,
		MerchantID: p.MerchantID,
		Channel:    string(p.Channel),
		Roles:      p.Roles,
	})
}

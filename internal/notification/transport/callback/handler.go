// Package callback 提供通知回执回调端点（ENT-10 NT-07）。
//
// 公网端点（挂在 c-api 边缘，不走登录鉴权），安全三件套：
//   - 验签：pkg/webhook HMAC-SHA256 + 时间窗 + nonce 防重放（多副本部署
//     需换 Redis NonceStore，见 ADR-0008）；
//   - 幂等：重复回执对已终态明细幂等吸收（app.ReceiptService）；
//   - 状态机：delivered / undelivered 单调推进，回执乱序不回退。
//
// 密钥缺失时构造失败（fail closed）：没有验签的回调端点等于公开写接口。
package callback

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	pkgwebhook "github.com/admin-frame/admin-frame/pkg/webhook"
)

// EnvCallbackSecret 是回执验签密钥的环境变量名（≥32 字节；ENT-07）。
const EnvCallbackSecret = "ADMIN_FRAME_NOTIFICATION_CALLBACK_SECRET"

// defaultTolerance 是签名时间窗。
const defaultTolerance = 5 * time.Minute

// Handler 处理渠道回执回调。
type Handler struct {
	receipts *app.ReceiptService
	verifier *pkgwebhook.Verifier
}

// NewHandler 构造回调 Handler；验签密钥取环境变量，缺失返回错误
// （装配期即失败，不允许出现无验签的公开写端点）。
func NewHandler(receipts *app.ReceiptService) (*Handler, error) {
	if receipts == nil {
		return nil, apperr.Internal("notification callback handler requires a receipt service", nil)
	}
	secret := strings.TrimSpace(os.Getenv(EnvCallbackSecret))
	if len(secret) < 32 {
		return nil, apperr.Internal(
			"notification callback requires env "+EnvCallbackSecret+" (>=32 bytes); unsigned callbacks are rejected (fail closed)", nil)
	}
	verifier, err := pkgwebhook.NewVerifier(pkgwebhook.Config{
		Keys:      [][]byte{[]byte(secret)},
		Tolerance: defaultTolerance,
		Nonce:     pkgwebhook.NewMemoryNonceStore(pkgwebhook.MemoryNonceStoreConfig{}),
	})
	if err != nil {
		return nil, apperr.Internal("notification callback verifier: %v", err)
	}
	return &Handler{receipts: receipts, verifier: verifier}, nil
}

// Receipt 处理 POST /v1/notification-callbacks/receipt。
//
// 请求体为归一化回执（各供应商格式由驱动适配层转换为统一契约，
// v1 先接平台自有签名渠道）：
//
//	{"channel":"sms","message_id":"...","delivered":true,
//	 "provider_code":"DELIVRD","summary":""}
//
// 未知消息 ID 与重复回执都返回 200（对供应商语义是「已受理」，
// 避免其重放风暴）；只有验签失败返回 401。
func (h *Handler) Receipt(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if _, err := h.verifier.VerifyRequest(r.Context(), r); err != nil {
		apiresp.FromError(w, apperr.Unauthorized("invalid webhook signature"), traceID)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		apiresp.FromError(w, apperr.BadRequest("invalid callback payload"), traceID)
		return
	}
	var payload receiptPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		apiresp.FromError(w, apperr.BadRequest("invalid callback payload"), traceID)
		return
	}
	if err := h.receipts.Apply(r.Context(), app.ReceiptInput{
		Channel:      domain.Channel(strings.TrimSpace(payload.Channel)),
		MessageID:    payload.MessageID,
		Delivered:    payload.Delivered,
		ProviderCode: payload.ProviderCode,
		Summary:      payload.Summary,
	}); err != nil {
		// 未知消息 ID 与重复回执按「已受理」返回（对供应商幂等，
		// 避免其重放风暴）；载荷非法返回 400。
		var appErr *apperr.AppError
		if errors.As(err, &appErr) && appErr.NumericCode() == apperr.CodeNotFound {
			apiresp.OK(w, map[string]string{"status": "ignored"}, traceID)
			return
		}
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, map[string]string{"status": "ok"}, traceID)
}

type receiptPayload struct {
	Channel      string `json:"channel"`
	MessageID    string `json:"message_id"`
	Delivered    bool   `json:"delivered"`
	ProviderCode string `json:"provider_code,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

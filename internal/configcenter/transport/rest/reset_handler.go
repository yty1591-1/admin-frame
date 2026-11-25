package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// ResetDefaultsHandler 处理「把取值非法的键重置为声明默认值」的内部请求。
//
// 与读路径共用服务身份校验（mTLS SAN / X-Service-Identity + 命名空间
// allowlist）：取值由服务端按声明默认值解析，调用方不能指定取值，避免把
// 启动自愈通道变成任意写入通道。
type ResetDefaultsHandler struct {
	reset     *app.BootstrapService
	deployEnv string
	withinTx  func(context.Context, func(context.Context) error) error
}

// ResetDefaultsHandlerConfig 是构造入参。
type ResetDefaultsHandlerConfig struct {
	Reset     *app.BootstrapService
	DeployEnv string
	WithinTx  func(context.Context, func(context.Context) error) error
}

// NewResetDefaultsHandler 构造重置默认值处理器。
func NewResetDefaultsHandler(cfg ResetDefaultsHandlerConfig) (*ResetDefaultsHandler, error) {
	if cfg.Reset == nil {
		return nil, apperr.Internal("reset defaults handler requires bootstrap service", nil)
	}
	withinTx := cfg.WithinTx
	if withinTx == nil {
		withinTx = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
	}
	return &ResetDefaultsHandler{reset: cfg.Reset, deployEnv: cfg.DeployEnv, withinTx: withinTx}, nil
}

// ResetDefaults 处理 POST /internal/v1/configs/repair/reset-defaults。
func (h *ResetDefaultsHandler) ResetDefaults(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	identity, err := resolveServiceIdentity(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Env  string   `json:"env"`
		Refs []string `json:"refs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	env := strings.TrimSpace(body.Env)
	if env == "" {
		env = h.deployEnv
	}
	if env != h.deployEnv {
		apiresp.FromError(w, apperr.BadRequest(
			fmt.Sprintf("env %q does not match config-api env %q", env, h.deployEnv)), traceID)
		return
	}
	for _, namespace := range namespacesOf(body.Refs) {
		if authErr := authorizeNamespace(h.deployEnv, identity, namespace); authErr != nil {
			apiresp.FromError(w, authErr, traceID)
			return
		}
	}
	actor := app.Actor{
		SubjectID: "service:" + identity,
		ActorType: "system",
		Channel:   "internal",
		TraceID:   traceID,
	}
	var result app.ResetResult
	err = h.withinTx(r.Context(), func(ctx context.Context) error {
		res, resetErr := h.reset.ResetToDefaults(ctx, app.ResetInput{
			Env: env, Refs: body.Refs, Actor: actor,
		})
		if resetErr != nil {
			return resetErr
		}
		result = res
		return nil
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, result, traceID)
}

// namespacesOf 从 namespace/key 引用中提取去重后的命名空间。
func namespacesOf(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		namespace, _, ok := strings.Cut(strings.TrimSpace(ref), "/")
		namespace = strings.TrimSpace(namespace)
		if !ok || namespace == "" {
			continue
		}
		if _, dup := seen[namespace]; dup {
			continue
		}
		seen[namespace] = struct{}{}
		out = append(out, namespace)
	}
	return out
}

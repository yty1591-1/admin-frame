package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// InstallHandler 处理 config-api 首次安装 API（ADR-0017）。
type InstallHandler struct {
	install   *app.InstallService
	deployEnv string
	withinTx  func(context.Context, func(context.Context) error) error
}

// InstallHandlerConfig 是 InstallHandler 装配参数。
type InstallHandlerConfig struct {
	Install   *app.InstallService
	DeployEnv string
	// WithinTx 可选；提供时 begin/apply/complete 在事务内执行。
	WithinTx func(context.Context, func(context.Context) error) error
}

// NewInstallHandler 构造安装 Handler。
func NewInstallHandler(cfg InstallHandlerConfig) (*InstallHandler, error) {
	if cfg.Install == nil {
		return nil, apperr.Internal("install handler requires install service", nil)
	}
	withinTx := cfg.WithinTx
	if withinTx == nil {
		withinTx = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
	}
	return &InstallHandler{
		install: cfg.Install, deployEnv: strings.TrimSpace(cfg.DeployEnv), withinTx: withinTx,
	}, nil
}

// Status 处理 GET /internal/v1/install/status。
func (h *InstallHandler) Status(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	state, err := h.install.Status(r.Context(), h.deployEnv)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, installStateDTO(state), traceID)
}

// Schema 处理 GET /internal/v1/install/schema。
//
// 返回向导渲染所需的字段清单与缺失必填键；与写路径一样要求 setup token，
// 因为 schema 会暴露键名、默认值与校验规则。
func (h *InstallHandler) Schema(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if err := h.install.RequireSetupToken(r.Header.Get(app.SetupTokenHeader())); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	schema, err := h.install.Schema(r.Context(), h.deployEnv)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, schema, traceID)
}

// Begin 处理 POST /internal/v1/install/begin。
func (h *InstallHandler) Begin(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if err := h.install.RequireSetupToken(r.Header.Get(app.SetupTokenHeader())); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Owner string `json:"owner"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var state domain.InstallState
	err := h.withinTx(r.Context(), func(ctx context.Context) error {
		var beginErr error
		state, beginErr = h.install.Begin(ctx, app.BeginInput{
			Env: h.deployEnv, Owner: body.Owner, Actor: setupActor(traceID),
		})
		return beginErr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, installStateDTO(state), traceID)
}

// Apply 处理 POST /internal/v1/install/apply。
func (h *InstallHandler) Apply(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if err := h.install.RequireSetupToken(r.Header.Get(app.SetupTokenHeader())); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Owner     string            `json:"owner"`
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && r.ContentLength != 0 {
		apiresp.FromError(w, apperr.BadRequest("invalid apply body"), traceID)
		return
	}
	var result app.ApplyResult
	err := h.withinTx(r.Context(), func(ctx context.Context) error {
		var applyErr error
		result, applyErr = h.install.Apply(ctx, app.ApplyInput{
			Env: h.deployEnv, Owner: body.Owner, Overrides: body.Overrides,
			Actor: setupActor(traceID),
		})
		return applyErr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, map[string]any{
		"namespaces": result.Namespaces,
		"item_count": result.ItemCount,
		"state":      installStateDTO(result.State),
	}, traceID)
}

// Complete 处理 POST /internal/v1/install/complete。
func (h *InstallHandler) Complete(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if err := h.install.RequireSetupToken(r.Header.Get(app.SetupTokenHeader())); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var body struct {
		Owner string `json:"owner"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var state domain.InstallState
	err := h.withinTx(r.Context(), func(ctx context.Context) error {
		var completeErr error
		state, completeErr = h.install.Complete(ctx, app.CompleteInput{
			Env: h.deployEnv, Owner: body.Owner, Actor: setupActor(traceID),
		})
		return completeErr
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, installStateDTO(state), traceID)
}

func setupActor(traceID string) app.Actor {
	return app.Actor{
		SubjectID: "system:setup-token",
		ActorType: "system",
		Channel:   "admin",
		TraceID:   traceID,
	}
}

func installStateDTO(state domain.InstallState) map[string]any {
	mode := app.InstallModeFirstInstall
	if state.Status == domain.InstallReady {
		mode = app.InstallModeRepair
	}
	out := map[string]any{
		"env":        state.Env,
		"mode":       mode,
		"status":     string(state.Status),
		"owner":      state.Owner,
		"version":    state.Version,
		"last_error": state.LastError,
		"updated_at": state.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if state.LeaseUntil != nil {
		out["lease_until"] = state.LeaseUntil.UTC().Format(time.RFC3339Nano)
	}
	if state.CompletedAt != nil {
		out["completed_at"] = state.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

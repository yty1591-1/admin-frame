package rest

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// InternalHandler 是 config-api 内部配置拉取 / 长轮询 Handler（ADR-0016）。
type InternalHandler struct {
	snapshots *app.SnapshotService
	watcher   *app.ReleaseWatcher
	secrets   *app.SecretService
	deployEnv string
}

// InternalHandlerConfig 是 InternalHandler 的装配参数。
type InternalHandlerConfig struct {
	Snapshots *app.SnapshotService
	Watcher   *app.ReleaseWatcher
	Secrets   *app.SecretService
	// DeployEnv 进程部署环境（决定 allowlist / 裸密钥门禁强度），非请求数据 env。
	DeployEnv string
}

// NewInternalHandler 构造内部 Handler；Snapshots 与 Watcher 必填。
func NewInternalHandler(cfg InternalHandlerConfig) (*InternalHandler, error) {
	if cfg.Snapshots == nil {
		return nil, apperr.Internal("internal config handler requires snapshot service", nil)
	}
	if cfg.Watcher == nil {
		return nil, apperr.Internal("internal config handler requires release watcher", nil)
	}
	return &InternalHandler{
		snapshots: cfg.Snapshots,
		watcher:   cfg.Watcher,
		secrets:   cfg.Secrets,
		deployEnv: strings.TrimSpace(cfg.DeployEnv),
	}, nil
}

// GetNamespaceConfig 处理 GET /internal/v1/configs/{env}/{namespace}?release_key=。
func (h *InternalHandler) GetNamespaceConfig(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	identity, err := resolveServiceIdentity(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	env := strings.TrimSpace(r.PathValue("env"))
	namespace := strings.TrimSpace(r.PathValue("namespace"))
	if err := authorizeNamespace(h.deployEnv, identity, namespace); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	snap, unchanged, err := h.snapshots.LoadIfChanged(r.Context(), env, namespace,
		strings.TrimSpace(r.URL.Query().Get("release_key")))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if unchanged {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	apiresp.OK(w, map[string]any{
		"env":             snap.Env,
		"namespace":       snap.Namespace,
		"release_id":      snap.ReleaseID,
		"release_no":      snap.ReleaseNo,
		"pointer_version": snap.PointerVersion,
		"release_key":     app.ReleaseKey(snap.ReleaseID, snap.PointerVersion),
		"values":          snap.Values,
		"generated_at":    formatTime(snap.GeneratedAt),
	}, traceID)
}

// Notifications 处理 GET /internal/v1/configs/notifications?env=&namespaces=platform:12,identity:5。
func (h *InternalHandler) Notifications(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	identity, err := resolveServiceIdentity(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	known, err := parseNamespaceVersions(r.URL.Query().Get("namespaces"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	for ns := range known {
		if authErr := authorizeNamespace(h.deployEnv, identity, ns); authErr != nil {
			apiresp.FromError(w, authErr, traceID)
			return
		}
	}
	timeout := app.DefaultLongPollTimeout
	if raw := strings.TrimSpace(r.URL.Query().Get("timeout")); raw != "" {
		parsed, perr := time.ParseDuration(raw)
		if perr != nil || parsed <= 0 || parsed > app.DefaultLongPollTimeout {
			apiresp.FromError(w, apperr.BadRequest("timeout must be a duration up to 60s"), traceID)
			return
		}
		timeout = parsed
	}
	notes, err := h.watcher.Wait(r.Context(), env, known, timeout)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if len(notes) == 0 {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	apiresp.OK(w, map[string]any{"items": notes}, traceID)
}

// ResolveSecrets 代理到既有密钥解析实现，便于 config-api 单 Handler 注册。
func (h *InternalHandler) ResolveSecrets(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	if h.secrets == nil {
		apiresp.FromError(w, apperr.Internal("secret service unavailable", nil), traceID)
		return
	}
	// 复用同包 Handler 的 ResolveSecrets 逻辑；传入部署 env 做 namespace 授权。
	delegate := &Handler{secrets: h.secrets, deployEnv: h.deployEnv}
	delegate.ResolveSecrets(w, r)
}

func parseNamespaceVersions(raw string) (map[string]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, apperr.BadRequest("namespaces query is required")
	}
	out := make(map[string]int64)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ns, verRaw, ok := strings.Cut(part, ":")
		ns = strings.TrimSpace(ns)
		verRaw = strings.TrimSpace(verRaw)
		if !ok || ns == "" || verRaw == "" {
			return nil, apperr.BadRequest("namespaces must be namespace:version pairs")
		}
		ver, err := strconv.ParseInt(verRaw, 10, 64)
		if err != nil || ver < 0 {
			return nil, apperr.BadRequest("invalid namespace version")
		}
		out[ns] = ver
	}
	if len(out) == 0 {
		return nil, apperr.BadRequest("namespaces query is required")
	}
	return out, nil
}

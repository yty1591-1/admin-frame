package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

const (
	// DefaultInstallLease 是 begin 默认租约时长。
	DefaultInstallLease = 15 * time.Minute
	// ResourceTypeInstall 审计资源类型。
	ResourceTypeInstall = "config_install"
	setupTokenEnv       = "CONFIG_INSTALL_SETUP_TOKEN" //nolint:gosec // G101: 环境变量名常量，非凭据。
	setupTokenHeader    = "X-Config-Install-Token"     //nolint:gosec // G101: 请求头名常量，非凭据。
)

// InstallService 管理首次安装状态机与 apply/complete（ADR-0017）。
type InstallService struct {
	install   domain.InstallRepository
	pointers  domain.PointerRepository
	bootstrap *BootstrapService
	secrets   *SecretService
	specs     *spec.Registry
	audit     auditContext
	ids       id.Generator
	now       func() time.Time
	lease     time.Duration
	setupTok  string
}

// InstallServiceConfig 是 InstallService 装配参数。
type InstallServiceConfig struct {
	Install   domain.InstallRepository
	Pointers  domain.PointerRepository
	Bootstrap *BootstrapService
	Secrets   *SecretService
	Specs     *spec.Registry
	Emitter   audit.Emitter
	IDs       id.Generator
	Options   Options
	Lease     time.Duration
	SetupTok  string
}

// NewInstallService 构造安装用例。
func NewInstallService(cfg InstallServiceConfig) (*InstallService, error) {
	switch {
	case cfg.Install == nil:
		return nil, apperr.Internal("install service requires install repository", nil)
	case cfg.Pointers == nil:
		return nil, apperr.Internal("install service requires pointer repository", nil)
	case cfg.Bootstrap == nil:
		return nil, apperr.Internal("install service requires bootstrap service", nil)
	case cfg.Specs == nil:
		return nil, apperr.Internal("install service requires key spec registry", nil)
	case cfg.Emitter == nil:
		return nil, apperr.Internal("install service requires audit emitter", nil)
	case cfg.IDs == nil:
		return nil, apperr.Internal("install service requires id generator", nil)
	}
	now := cfg.Options.Now
	if now == nil {
		now = time.Now
	}
	lease := cfg.Lease
	if lease <= 0 {
		lease = DefaultInstallLease
	}
	tok := strings.TrimSpace(cfg.SetupTok)
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv(setupTokenEnv))
	}
	return &InstallService{
		install:   cfg.Install,
		pointers:  cfg.Pointers,
		bootstrap: cfg.Bootstrap,
		secrets:   cfg.Secrets,
		specs:     cfg.Specs,
		audit:     auditContext{emitter: cfg.Emitter, ids: cfg.IDs, service: cfg.Options.Service, now: now},
		ids:       cfg.IDs,
		now:       now,
		lease:     lease,
		setupTok:  tok,
	}, nil
}

// SetupTokenHeader 返回写路径鉴权头名。
func SetupTokenHeader() string { return setupTokenHeader }

// RequireSetupToken 校验 setup token；prod-like 未配置 token 时拒绝。
func (s *InstallService) RequireSetupToken(provided string) error {
	want := strings.TrimSpace(s.setupTok)
	got := strings.TrimSpace(provided)
	if want == "" {
		return apperr.Unauthorized("setup token is not configured")
	}
	if !secureTokenEqual(want, got) {
		return apperr.Unauthorized("invalid setup token")
	}
	return nil
}

// RequireSetupTokenConfigured 在启动时断言 token 已配置。
//
// 所有环境统一走安装流程（ADR-0018），因此 token 不再按 prod-like 豁免：
// 缺失时安装态只能停在 pending，业务进程无法就绪。
func RequireSetupTokenConfigured(env string) error {
	if strings.TrimSpace(os.Getenv(setupTokenEnv)) == "" {
		return fmt.Errorf("%s is required for env %q (install wizard is the only seeding path)", setupTokenEnv, env)
	}
	return nil
}

// EnsureState 保证 env 有安装态行；initial 为无行时的初始 status。
func (s *InstallService) EnsureState(ctx context.Context, env string, initial domain.InstallStatus) (domain.InstallState, error) {
	if !domain.ValidEnv(env) {
		return domain.InstallState{}, apperr.BadRequest("invalid env")
	}
	if !domain.ValidInstallStatus(initial) {
		return domain.InstallState{}, apperr.BadRequest("invalid install status")
	}
	now := s.now().UTC()
	return s.install.Ensure(ctx, domain.InstallState{
		Env: env, Status: initial, Mode: domain.InstallModeFirstInstall, Version: 1, UpdatedAt: now,
	})
}

// Status 返回当前安装态。
func (s *InstallService) Status(ctx context.Context, env string) (domain.InstallState, error) {
	state, err := s.install.Get(ctx, env)
	if err != nil {
		return domain.InstallState{}, translate(err)
	}
	return state, nil
}

// Gaps 返回当前阻塞启动的非敏感必填键；与 repair 冻结白名单同源。
func (s *InstallService) Gaps(ctx context.Context, env string) ([]RequiredGap, error) {
	if !domain.ValidEnv(env) {
		return nil, apperr.BadRequest("invalid env")
	}
	return s.bootstrap.MissingRequired(ctx, env)
}

// BeginInput 是 begin 入参。
type BeginInput struct {
	Env   string
	Owner string
	// Reason 取 first_install 或 repair；空值时按当前状态推断（ready → repair）。
	Reason string
	Actor  Actor
}

// Begin 抢安装锁：pending|failed → installing(first_install)；ready → installing(repair)。
//
// repair 轮次在 begin 时冻结阻塞键白名单，apply 只接受白名单内的键（ADR-0018）。
func (s *InstallService) Begin(ctx context.Context, in BeginInput) (domain.InstallState, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.InstallState{}, err
	}
	if !domain.ValidEnv(in.Env) {
		return domain.InstallState{}, apperr.BadRequest("invalid env")
	}
	owner := strings.TrimSpace(in.Owner)
	if owner == "" {
		owner = newInstallOwner()
	}
	cur, err := s.install.Get(ctx, in.Env)
	if err != nil {
		return domain.InstallState{}, translate(err)
	}
	mode, repairKeys, err := s.beginMode(ctx, in, cur)
	if err != nil {
		return domain.InstallState{}, err
	}
	now := s.now().UTC()
	if cur.Status == domain.InstallInstalling && cur.LeaseActive(now) && cur.Owner != owner {
		return cur, apperr.New(apperr.CodeInstallInProgress,
			fmt.Sprintf("install in progress by %s", cur.Owner), nil)
	}
	switch cur.Status {
	case domain.InstallPending, domain.InstallFailed, domain.InstallInstalling, domain.InstallReady:
		// ok；ready 仅在 mode=repair 时到达这里。
	default:
		return domain.InstallState{}, apperr.Conflict("cannot begin install from status " + string(cur.Status))
	}
	leaseUntil := now.Add(s.lease)
	next := cur
	next.Status = domain.InstallInstalling
	next.Mode = mode
	next.RepairKeys = repairKeys
	next.Owner = owner
	next.LeaseUntil = &leaseUntil
	next.LastError = ""
	next.UpdatedAt = now
	next.CompletedAt = nil
	updated, err := s.install.CompareAndSwap(ctx, next, cur.Version)
	if err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return domain.InstallState{}, apperr.New(apperr.CodeInstallInProgress,
				"install begin lost race", nil)
		}
		return domain.InstallState{}, translate(err)
	}
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigInstallBegin, ResourceTypeInstall, in.Env,
		audit.OutcomeSuccess, "", map[string]string{
			"owner": owner, "status": string(updated.Status),
			"mode": string(mode), "repair_keys": strings.Join(repairKeys, ","),
		})
	return updated, nil
}

// beginMode 解析本轮 begin 的性质，并在 repair 时冻结阻塞键白名单。
func (s *InstallService) beginMode(ctx context.Context, in BeginInput,
	cur domain.InstallState) (domain.InstallMode, []string, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		if cur.Status == domain.InstallReady {
			reason = string(domain.InstallModeRepair)
		} else {
			reason = string(domain.InstallModeFirstInstall)
		}
	}
	mode := domain.InstallMode(reason)
	if !domain.ValidInstallMode(mode) {
		return "", nil, apperr.BadRequest(`reason must be "first_install" or "repair"`)
	}
	if mode == domain.InstallModeFirstInstall {
		if cur.Status == domain.InstallReady {
			return "", nil, apperr.New(apperr.CodeInstallCompleted, "install already completed", nil)
		}
		return mode, nil, nil
	}
	if cur.Status != domain.InstallReady {
		return "", nil, apperr.BadRequest("repair requires install status ready")
	}
	gaps, err := s.bootstrap.MissingRequired(ctx, in.Env)
	if err != nil {
		return "", nil, err
	}
	if len(gaps) == 0 {
		return "", nil, apperr.New(apperr.CodeInstallCompleted,
			"no blocking keys to repair", nil)
	}
	keys := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		keys = append(keys, gap.KeyRef())
	}
	return mode, keys, nil
}

// ApplyInput 是 apply 入参。
type ApplyInput struct {
	Env       string
	Owner     string
	Overrides map[string]string
	Actor     Actor
}

// ApplyResult 是 apply 汇总。
type ApplyResult struct {
	Namespaces int
	ItemCount  int
	// RemainingGaps 是 apply 之后仍未补齐的阻塞键；complete 前必须为空。
	RemainingGaps []RequiredGap
	State         domain.InstallState
}

// Apply 在持锁状态下播种（不覆盖已有值）；续约租约。
//
// first_install 接受任意已声明键；repair 只接受 begin 时冻结的白名单键。
func (s *InstallService) Apply(ctx context.Context, in ApplyInput) (ApplyResult, error) {
	if err := requireActor(in.Actor); err != nil {
		return ApplyResult{}, err
	}
	state, err := s.requireHolder(ctx, in.Env, in.Owner)
	if err != nil {
		return ApplyResult{}, err
	}
	overrides, err := s.filterOverrides(state, in.Overrides)
	if err != nil {
		return ApplyResult{}, err
	}
	actor := in.Actor
	itemCount := 0
	for _, namespace := range s.specs.Namespaces() {
		result, ensureErr := s.bootstrap.EnsureBaseline(ctx, BootstrapInput{
			Env: in.Env, Namespace: namespace, Actor: actor,
			Overrides: overrides, Repair: state.Mode == domain.InstallModeRepair,
		})
		if ensureErr != nil {
			return s.failApply(ctx, state, in, ensureErr)
		}
		itemCount += result.ItemCount
	}
	if s.secrets != nil {
		if secErr := s.secrets.EnsureAutoSecrets(ctx, in.Env, s.specs, actor); secErr != nil {
			return s.failApply(ctx, state, in, secErr)
		}
	}
	gaps, err := s.bootstrap.MissingRequired(ctx, in.Env)
	if err != nil {
		return s.failApply(ctx, state, in, err)
	}
	now := s.now().UTC()
	leaseUntil := now.Add(s.lease)
	next := state
	next.LeaseUntil = &leaseUntil
	next.UpdatedAt = now
	next.LastError = ""
	updated, err := s.install.CompareAndSwap(ctx, next, state.Version)
	if err != nil {
		return ApplyResult{}, translate(err)
	}
	_ = s.audit.emit(ctx, actor, audit.ActionConfigInstallApply, ResourceTypeInstall, in.Env,
		audit.OutcomeSuccess, "", map[string]string{
			"namespaces": fmt.Sprintf("%d", len(s.specs.Namespaces())),
			"item_count": fmt.Sprintf("%d", itemCount),
			"mode":       string(state.Mode),
			"remaining":  fmt.Sprintf("%d", len(gaps)),
		})
	return ApplyResult{
		Namespaces:    len(s.specs.Namespaces()),
		ItemCount:     itemCount,
		RemainingGaps: gaps,
		State:         updated,
	}, nil
}

// filterOverrides 在 repair 轮次把提交值收窄到冻结白名单；首装原样返回。
func (s *InstallService) filterOverrides(state domain.InstallState,
	overrides map[string]string) (map[string]string, error) {
	if state.Mode != domain.InstallModeRepair || len(overrides) == 0 {
		return overrides, nil
	}
	allowed := make(map[string]struct{}, len(state.RepairKeys))
	for _, key := range state.RepairKeys {
		allowed[key] = struct{}{}
	}
	filtered := make(map[string]string, len(overrides))
	rejected := make([]string, 0)
	for key, value := range overrides {
		namespace, ok := s.namespaceFor(key)
		if !ok {
			rejected = append(rejected, key)
			continue
		}
		if _, ok := allowed[namespace+"/"+key]; !ok {
			rejected = append(rejected, key)
			continue
		}
		filtered[key] = value
	}
	if len(rejected) > 0 {
		return nil, apperr.BadRequest(fmt.Sprintf(
			"repair only accepts frozen blocking keys; rejected: %s", strings.Join(rejected, ", ")))
	}
	return filtered, nil
}

// namespaceFor 返回声明该键的 namespace。
func (s *InstallService) namespaceFor(key string) (string, bool) {
	for _, namespace := range s.specs.Namespaces() {
		if _, ok := s.specs.Lookup(namespace, key); ok {
			return namespace, true
		}
	}
	return "", false
}

func (s *InstallService) failApply(ctx context.Context, state domain.InstallState, in ApplyInput, cause error) (ApplyResult, error) {
	now := s.now().UTC()
	next := state
	next.Status = domain.InstallFailed
	next.Owner = ""
	next.LeaseUntil = nil
	next.LastError = truncateErr(cause.Error(), 512)
	next.UpdatedAt = now
	_, _ = s.install.CompareAndSwap(ctx, next, state.Version)
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigInstallFail, ResourceTypeInstall, in.Env,
		audit.OutcomeFailure, next.LastError, nil)
	return ApplyResult{}, cause
}

// CompleteInput 是 complete 入参。
type CompleteInput struct {
	Env   string
	Owner string
	Actor Actor
}

// Complete 校验关键 namespace 已有 pointer 后进入 ready。
func (s *InstallService) Complete(ctx context.Context, in CompleteInput) (domain.InstallState, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.InstallState{}, err
	}
	state, err := s.requireHolder(ctx, in.Env, in.Owner)
	if err != nil {
		return domain.InstallState{}, err
	}
	for _, namespace := range s.specs.Namespaces() {
		if _, getErr := s.pointers.Get(ctx, in.Env, namespace); getErr == nil {
			continue
		}
		msg := fmt.Sprintf("namespace %q missing release pointer", namespace)
		now := s.now().UTC()
		next := state
		next.Status = domain.InstallFailed
		next.Owner = ""
		next.LeaseUntil = nil
		next.LastError = msg
		next.UpdatedAt = now
		_, _ = s.install.CompareAndSwap(ctx, next, state.Version)
		_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigInstallFail, ResourceTypeInstall, in.Env,
			audit.OutcomeFailure, msg, nil)
		return domain.InstallState{}, apperr.BadRequest(msg)
	}
	gaps, err := s.bootstrap.MissingRequired(ctx, in.Env)
	if err != nil {
		return domain.InstallState{}, err
	}
	if len(gaps) > 0 {
		keys := make([]string, 0, len(gaps))
		for _, gap := range gaps {
			keys = append(keys, gap.KeyRef())
		}
		return domain.InstallState{}, apperr.BadRequest(
			"install incomplete; blocking keys remain: " + strings.Join(keys, ", "))
	}
	now := s.now().UTC()
	completed := now
	next := state
	next.Status = domain.InstallReady
	next.Owner = ""
	next.LeaseUntil = nil
	next.LastError = ""
	next.UpdatedAt = now
	next.CompletedAt = &completed
	updated, err := s.install.CompareAndSwap(ctx, next, state.Version)
	if err != nil {
		return domain.InstallState{}, translate(err)
	}
	_ = s.audit.emit(ctx, in.Actor, audit.ActionConfigInstallComplete, ResourceTypeInstall, in.Env,
		audit.OutcomeSuccess, "", map[string]string{
			"status": "ready", "mode": string(state.Mode),
		})
	return updated, nil
}

func (s *InstallService) requireHolder(ctx context.Context, env, owner string) (domain.InstallState, error) {
	if !domain.ValidEnv(env) {
		return domain.InstallState{}, apperr.BadRequest("invalid env")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return domain.InstallState{}, apperr.BadRequest("owner is required")
	}
	cur, err := s.install.Get(ctx, env)
	if err != nil {
		return domain.InstallState{}, translate(err)
	}
	if cur.Status == domain.InstallReady {
		return domain.InstallState{}, apperr.New(apperr.CodeInstallCompleted,
			"install already completed", nil)
	}
	now := s.now().UTC()
	if cur.Status != domain.InstallInstalling || !cur.LeaseActive(now) || cur.Owner != owner {
		return domain.InstallState{}, apperr.New(apperr.CodeInstallInProgress,
			"caller does not hold install lease", nil)
	}
	return cur, nil
}

func newInstallOwner() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "install-" + hex.EncodeToString(buf[:])
}

func secureTokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func truncateErr(msg string, limit int) string {
	msg = strings.TrimSpace(msg)
	if limit <= 0 || len(msg) <= limit {
		return msg
	}
	return msg[:limit]
}

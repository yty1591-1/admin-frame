package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/featureflag/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 缓存时效（feature-flag-plan.md §0.5）：kill switch 面短（「秒级开关」
// 判据 ≤5s），规则面长（规则调整不要求秒级）。
const (
	killSwitchTTL = 5 * time.Second
	ruleTTL       = 30 * time.Second
)

// EvaluationObserver 是评估链路的可观测端口：指标埋点与缓存健康，
// nil 安全由构造保证（noop）。
type EvaluationObserver interface {
	// EvalObserved 记录一次评估（result: hit / miss / killed / unknown）。
	EvalObserved(flagKey, result string)
	// CacheObserved 记录缓存命中状态（hit / miss / expired）。
	CacheObserved(kind, state string)
}

type noopEvaluationObserver struct{}

// EvalObserved noop：不做任何记录。
func (noopEvaluationObserver) EvalObserved(string, string) {}

// CacheObserved noop：不做任何记录。
func (noopEvaluationObserver) CacheObserved(string, string) {}

// EvaluationService 是评估引擎（domain.Evaluator 实现）：
// kill switch → 规则按序 → default_enabled，进程内两级 TTL 缓存。
//
// 并发安全；缓存条目惰性过期（读时判定，无后台清扫——flag 量级有限，
// map 增长由 flag 生命周期约束）。
type EvaluationService struct {
	repos    domain.Repository
	bucket   func(flagKey, subjectID string, bucketCount int) int
	observer EvaluationObserver
	now      func() time.Time

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	killSwitch bool
	rules      []domain.Rule
	def        bool
	killAt     time.Time
	ruleAt     time.Time
}

// EvaluationOptions 构造参数。
type EvaluationOptions struct {
	// Bucket 分桶函数；nil 使用 SHA-256 稳定哈希（infrastructure.Bucket）。
	// 测试注入确定性哈希。
	Bucket func(flagKey, subjectID string, bucketCount int) int
	// Observer 可观测端口；nil 使用 noop。
	Observer EvaluationObserver
	// Now 时钟；测试注入。
	Now func() time.Time
}

// NewEvaluationService 构造评估服务；仓储必填。
func NewEvaluationService(repos domain.Repository, opts EvaluationOptions) (*EvaluationService, error) {
	if repos == nil {
		return nil, apperr.Internal("evaluation service requires a flag repository", nil)
	}
	bucket := opts.Bucket
	if bucket == nil {
		bucket = domain.Bucket
	}
	observer := opts.Observer
	if observer == nil {
		observer = noopEvaluationObserver{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &EvaluationService{
		repos:    repos,
		bucket:   bucket,
		observer: observer,
		now:      now,
		cache:    make(map[string]cacheEntry),
	}, nil
}

// IsEnabled 实现 domain.Evaluator（feature-flag-plan.md §0.3 评估顺序）：
//
//  1. kill switch：缓存面 5s；enabled=false 无条件 false（「关闭即回退」）；
//  2. 规则按序：首个命中生效；percentage 维度按稳定哈希分桶，subject
//     为空恒不命中（fail closed，评估必须可复现）；
//  3. 均未命中回退 default_enabled；
//  4. 未知 key / 仓储故障：false（fail closed）并计 unknown / error 观测。
func (s *EvaluationService) IsEnabled(ctx context.Context, key string, evalCtx domain.EvalContext) bool {
	flagKey := strings.TrimSpace(key)
	if flagKey == "" {
		return false
	}
	entry, ok := s.snapshot(ctx, flagKey)
	if !ok {
		s.observer.EvalObserved(flagKey, "unknown")
		return false
	}
	// ① kill switch（缓存面 5s：开关回退是应急动作，时效优先）。
	if !entry.killSwitch {
		s.observer.EvalObserved(flagKey, "killed")
		return false
	}
	// ② 规则按序（缓存面 30s）。
	for _, rule := range entry.rules {
		switch rule.Kind() {
		case "channel", "tenant":
			matched, err := rule.Matches(evalCtx.Channel, evalCtx.TenantID)
			if err != nil || !matched {
				continue
			}
			s.observer.EvalObserved(flagKey, "hit")
			return true
		case "percentage":
			if strings.TrimSpace(evalCtx.SubjectID) == "" {
				continue // fail closed：无主体不做随机评估。
			}
			if s.bucket(flagKey, evalCtx.SubjectID, domain.MaxPercentage) < rule.Percentage {
				s.observer.EvalObserved(flagKey, "hit")
				return true
			}
		default: // empty 规则：占位，跳过。
		}
	}
	// ③ 回退默认值。
	if entry.def {
		s.observer.EvalObserved(flagKey, "hit")
		return true
	}
	s.observer.EvalObserved(flagKey, "miss")
	return false
}

// snapshot 取缓存或回源；仓储与解析故障返回 !ok（fail closed）。
func (s *EvaluationService) snapshot(ctx context.Context, flagKey string) (cacheEntry, bool) {
	now := s.now()
	s.mu.RLock()
	if entry, ok := s.cache[flagKey]; ok {
		killFresh := now.Sub(entry.killAt) < killSwitchTTL
		ruleFresh := now.Sub(entry.ruleAt) < ruleTTL
		s.mu.RUnlock()
		if killFresh && ruleFresh {
			s.observer.CacheObserved("flag", "hit")
			return entry, true
		}
		s.observer.CacheObserved("flag", "expired")
		// 过期：穿透回源刷新（下行覆盖）。
	} else {
		s.mu.RUnlock()
		s.observer.CacheObserved("flag", "miss")
	}

	flag, err := s.repos.Get(ctx, flagKey)
	if err != nil {
		return cacheEntry{}, false
	}
	entry := cacheEntry{
		killSwitch: flag.Status == domain.StatusEnabled,
		def:        flag.DefaultEnabled,
		killAt:     now,
		ruleAt:     now,
		rules:      append([]domain.Rule(nil), flag.Rules...),
	}
	s.mu.Lock()
	s.cache[flagKey] = entry
	s.mu.Unlock()
	return entry, true
}

// Invalidate 清空缓存（管理端变更后调用；跨副本靠 TTL 自然过期）。
func (s *EvaluationService) Invalidate() {
	s.mu.Lock()
	s.cache = make(map[string]cacheEntry)
	s.mu.Unlock()
}

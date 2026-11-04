package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 黑名单相关审计动作与资源类型。
const (
	// ActionBlacklistAdd 加入黑名单（投诉 / 退信 / 手工）。
	ActionBlacklistAdd = "notification.blacklist.add"
	// ActionBlacklistRemove 移除黑名单条目。
	ActionBlacklistRemove = "notification.blacklist.remove"
	// ResourceTypeBlacklist 黑名单资源类型。
	ResourceTypeBlacklist = "notification_blacklist"
)

// TargetHasher 把收件人地址映射为黑名单键（加盐 HMAC 摘要的十六进制串）。
//
// 端口化理由：盐值来自密钥托管（ENT-07 / KMS），通知域不接触明文密钥；
// 生产装配注入 HMAC 实现，单测注入固定函数。
type TargetHasher func(target string) string

// HMACHasher 返回加盐 HMAC-SHA256 实现（生产默认）。
// 盐为空时构造失败：无盐 HMAC 等于可离线枚举的普通哈希，等于没脱敏。
// 每次调用新建 MAC 实例（hmac.New 的 Write 不可并发复用）。
func HMACHasher(salt []byte) (TargetHasher, error) {
	if len(salt) == 0 {
		return nil, apperr.Internal("notification target hasher requires a non-empty salt", nil)
	}
	return func(target string) string {
		h := hmac.New(sha256.New, salt)
		h.Write([]byte(target))
		return hex.EncodeToString(h.Sum(nil))
	}, nil
}

// BlacklistService 是渠道黑名单用例：加入、移除、判定。
//
// 目标地址只以加盐 HMAC 落库（领域层强约束 64 位十六进制），
// 投诉 / 退信回执与运营手工录入都经同一入口。
type BlacklistService struct {
	blacklist domain.BlacklistRepository
	hasher    TargetHasher
	audit     auditContext
	ids       id.Generator
	now       func() time.Time
}

// NewBlacklistService 构造黑名单用例；hasher 必填（无盐即无脱敏）。
func NewBlacklistService(blacklist domain.BlacklistRepository, hasher TargetHasher,
	ids id.Generator, emitter audit.Emitter, opts Options) (*BlacklistService, error) {
	switch {
	case blacklist == nil:
		return nil, apperr.Internal("notification blacklist service requires a blacklist repository", nil)
	case hasher == nil:
		return nil, apperr.Internal("notification blacklist service requires a target hasher", nil)
	case ids == nil:
		return nil, apperr.Internal("notification blacklist service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("notification blacklist service requires an audit emitter", nil)
	}
	now := time.Now
	return &BlacklistService{
		blacklist: blacklist,
		hasher:    hasher,
		audit:     auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		ids:       ids,
		now:       now,
	}, nil
}

// AddBlacklistInput 是加入黑名单入参。
type AddBlacklistInput struct {
	// TenantID 租户隔离键；平台级条目传 domain.PlatformTenantID。
	TenantID string
	// Channel 渠道。
	Channel domain.Channel
	// Target 明文目标地址（仅入参，不落库、不入审计）。
	Target string
	// Source 来源（complaint / bounce / manual）。
	Source domain.BlacklistSource
	// Reason 运营可读原因。
	Reason string
	// Actor 操作者。
	Actor Actor
}

// Add 加入黑名单（幂等）；动作入审计，attrs 只记 HMAC 前缀（排障可对账、不可还原）。
func (s *BlacklistService) Add(ctx context.Context, in AddBlacklistInput) (domain.Blacklist, error) {
	if strings.TrimSpace(in.Target) == "" {
		return domain.Blacklist{}, apperr.BadRequest("blacklist target is required")
	}
	targetHMAC := s.hasher(in.Target)
	b, err := domain.NewBlacklist(in.TenantID, in.Channel, targetHMAC, in.Source, in.Reason, in.Actor.SubjectID, s.now())
	if err != nil {
		return domain.Blacklist{}, translateIfNeeded(err)
	}
	if err := s.blacklist.Add(ctx, b); err != nil {
		return domain.Blacklist{}, apperr.Internal("save notification blacklist failed", err)
	}
	attrs := map[string]string{
		"channel":     string(in.Channel),
		"target_hmac": shortHMAC(targetHMAC),
		"source":      string(in.Source),
	}
	if err := s.audit.emit(ctx, in.Actor, ActionBlacklistAdd, ResourceTypeBlacklist,
		b.TargetHMAC, audit.OutcomeSuccess, in.Reason, attrs); err != nil {
		return domain.Blacklist{}, apperr.Internal("emit blacklist audit failed", err)
	}
	return b, nil
}

// Remove 移除黑名单条目（按 HMAC 定位，管理端持有列表中的摘要）。
func (s *BlacklistService) Remove(ctx context.Context, tenantID string, channel domain.Channel,
	targetHMAC string, actor Actor) error {
	if err := s.blacklist.Remove(ctx, tenantID, channel, targetHMAC); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("notification blacklist entry not found")
		}
		return apperr.Internal("remove notification blacklist failed", err)
	}
	attrs := map[string]string{
		"channel":     string(channel),
		"target_hmac": shortHMAC(targetHMAC),
	}
	if err := s.audit.emit(ctx, actor, ActionBlacklistRemove, ResourceTypeBlacklist,
		targetHMAC, audit.OutcomeSuccess, "", attrs); err != nil {
		return apperr.Internal("emit blacklist audit failed", err)
	}
	return nil
}

// List 列出租户黑名单（含平台级条目）。
func (s *BlacklistService) List(ctx context.Context, tenantID string) ([]domain.Blacklist, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, apperr.BadRequest("tenant id is required")
	}
	items, err := s.blacklist.List(ctx, tenantID)
	if err != nil {
		return nil, apperr.Internal("list notification blacklist failed", err)
	}
	return items, nil
}

// IsBlocked 是编排管道第 2 步的黑名单判定入口（平台级 + 租户级都查）。
func (s *BlacklistService) IsBlocked(ctx context.Context, tenantID string, channel domain.Channel, target string) (bool, error) {
	if strings.TrimSpace(target) == "" {
		return false, apperr.BadRequest("blacklist target is required")
	}
	hit, err := s.blacklist.Exists(ctx, tenantID, channel, s.hasher(target))
	if err != nil {
		return false, apperr.Internal("check notification blacklist failed", err)
	}
	return hit, nil
}

// shortHMAC 摘要前 8 位：审计 attrs 里够对账、不可穷举还原。
func shortHMAC(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

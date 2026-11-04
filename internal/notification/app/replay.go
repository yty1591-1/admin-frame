package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 补偿相关审计动作与资源类型。
const (
	// ActionDLQReplay 重放失败通知（补偿动作，写操作）。
	ActionDLQReplay = "notification.dlq.replay"
	// ResourceTypeRequest 通知单资源类型。
	ResourceTypeRequest = "notification_request"
)

// DLQReader 是死信载荷读取端口：失败通知的唯一载荷快照存于
// async_dlq（Outbox DLQ），Replay 据此重建事件——通知表按 PII
// 最小化原则不存参数明文，补偿不破坏这一约束。
type DLQReader interface {
	// PayloadBySource 按原事件 ID 读取死信载荷；不存在返回 ErrNotFound。
	PayloadBySource(ctx context.Context, sourceEventID string) ([]byte, error)
}

// ReplayService 是失败通知的补偿用例。
//
// 语义（规划 §2.F）：重放生成**新事件 + 新通知单**，保留原请求作审计痕迹；
// 新幂等键由新请求 ID 派生（notify:replay:<newID>），天然防与原请求去重碰撞。
type ReplayService struct {
	requests   domain.RequestRepository
	dispatcher *OutboxDispatcher
	dlq        DLQReader
	audit      auditContext
	ids        id.Generator
	now        func() time.Time
	defaults   Options
}

// NewReplayService 构造补偿用例；仓储 / 出口 / DLQ / 审计缺一不可。
func NewReplayService(requests domain.RequestRepository, dispatcher *OutboxDispatcher,
	dlq DLQReader, ids id.Generator, emitter audit.Emitter, opts Options) (*ReplayService, error) {
	switch {
	case requests == nil:
		return nil, apperr.Internal("notification replay service requires a request repository", nil)
	case dispatcher == nil:
		return nil, apperr.Internal("notification replay service requires a dispatcher", nil)
	case dlq == nil:
		return nil, apperr.Internal("notification replay service requires a dlq reader", nil)
	case ids == nil:
		return nil, apperr.Internal("notification replay service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("notification replay service requires an audit emitter", nil)
	}
	now := time.Now
	return &ReplayService{
		requests:   requests,
		dispatcher: dispatcher,
		dlq:        dlq,
		audit:      auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		ids:        ids,
		now:        now,
		defaults:   opts,
	}, nil
}

// ReplayInput 是重放入参。
type ReplayInput struct {
	// RequestID 原失败通知单 ID。
	RequestID string
	// Actor 操作者（补偿是显式动作，必须留痕到人）。
	Actor Actor
}

// Replay 重放一条失败通知；返回新通知单 ID。
//
// 流程：校验原请求为 failed 终态 → 读 DLQ 载荷（原渲染参数与地址）
// → 换新请求 ID / 幂等键 / 有效期 → 重新入 Outbox → 审计。
func (s *ReplayService) Replay(ctx context.Context, in ReplayInput) (string, error) {
	trimmed := strings.TrimSpace(in.RequestID)
	if trimmed == "" {
		return "", apperr.BadRequest("request id is required")
	}
	original, err := s.requests.Get(ctx, trimmed)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", apperr.NotFound("notification request not found")
		}
		return "", apperr.Internal("load notification request failed", err)
	}
	if original.Status != domain.RequestFailed {
		return "", apperr.New(apperr.CodeConflict,
			"only failed notifications can be replayed, current status "+string(original.Status), nil)
	}
	raw, err := s.dlq.PayloadBySource(ctx, original.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", apperr.NotFound("dead letter payload not found for request " + original.ID)
		}
		return "", apperr.Internal("load dead letter payload failed", err)
	}
	var payload RequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", apperr.Internal("decode dead letter payload failed", err)
	}

	newID, err := s.ids.Next()
	if err != nil {
		return "", apperr.Internal("generate replay request id failed", err)
	}
	newRequestID := fmt.Sprintf("%d", newID)
	// 新幂等键由新请求 ID 派生：与原请求去重键永不碰撞。
	payload.RequestID = newRequestID
	payload.IdempotencyKey = "notify:replay:" + newRequestID
	payload.ReplayOf = original.ID
	ttl := s.defaults.DefaultNotificationTTL
	if ttl <= 0 {
		ttl = domain.DefaultNotificationTTL
	}
	payload.ExpiresAt = s.now().Add(ttl)
	if err := s.dispatcher.Dispatch(ctx, payload); err != nil {
		return "", apperr.Internal("dispatch replay event failed", err)
	}
	if err := s.audit.emit(ctx, in.Actor, ActionDLQReplay, ResourceTypeRequest, original.ID,
		audit.OutcomeSuccess, "", map[string]string{
			"scene":          original.Scene,
			"replay_request": newRequestID,
		}); err != nil {
		return "", apperr.Internal("emit replay audit failed", err)
	}
	return newRequestID, nil
}

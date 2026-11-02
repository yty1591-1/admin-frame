package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// DefaultAuditTable 是审计事件的默认落库表名。
//
// 表由 migrations/000006 从 identity_audit_event 重命名为平台级 audit_event：
// 身份、IAM 与后续业务域的事件共用同一张追加写表，查询与保留走 internal/audit 域。
const DefaultAuditTable = "audit_event"

// MultiEmitter 按顺序把同一事件写入多个出口（如审计库 + 结构化日志）。
//
// 任一口失败即返回错误，由 Outbox Relay 重试；已成功的出口依赖事件 ID 幂等。
type MultiEmitter []Emitter

// Emit 依次调用各出口。
func (m MultiEmitter) Emit(ctx context.Context, event Event) error {
	for _, emitter := range m {
		if emitter == nil {
			continue
		}
		if err := emitter.Emit(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// SQLEmitter 把审计事件追加写入独立审计表（AUD-06 / AUD-07）。
//
// 表约束：event_id 唯一（重复投递幂等）；应用只 INSERT，不提供 UPDATE/DELETE 路径。
// 保留期由运维按 AUD-07 基线（安全 / 登录 / 权限 ≥180 天）配置，不在此实现业务删除。
type SQLEmitter struct {
	// DB 审计库连接；nil 时失败关闭，禁止静默丢弃。
	DB *sql.DB
	// Table 目标表名；空值使用 DefaultAuditTable。
	Table string
}

// Emit 校验并写入审计表；同一 event_id 重复写入视为成功。
func (s SQLEmitter) Emit(ctx context.Context, event Event) error {
	if s.DB == nil {
		return apperr.Unavailable("audit database is not configured")
	}
	if err := event.Validate(); err != nil {
		return apperr.Internal("invalid audit event", err)
	}
	table := s.Table
	if strings.TrimSpace(table) == "" {
		table = DefaultAuditTable
	}
	attrs, err := encodeAttrs(event.Attrs)
	if err != nil {
		return apperr.Internal("encode audit attrs", err)
	}
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO `+table+`
		(event_id, action, actor_id, actor_type, channel, merchant_id,
		 resource_type, resource_id, outcome, occurred_at, trace_id, service, reason, attrs, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE event_id = event_id`,
		event.EventID, event.Action, event.ActorID, nullString(event.ActorType),
		event.Channel, event.MerchantID, event.ResourceType, event.ResourceID,
		string(event.Outcome), event.OccurredAt.UTC(), event.TraceID, event.Service,
		nullString(event.Reason), attrs, now)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

// encodeAttrs 把附加字段编码为 JSON；空 map 返回 NULL，避免无意义载荷。
func encodeAttrs(attrs map[string]string) (any, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	return string(payload), nil
}

// nullString 把空串写为 NULL，便于审计列建立稀疏索引与展示。
func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

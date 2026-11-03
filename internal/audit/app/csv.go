package app

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// exportHeader 是导出 CSV 的列顺序，与 exportRow 一一对应。
var exportHeader = []string{
	"event_id", "occurred_at", "action", "outcome", "channel",
	"actor_id", "actor_type", "merchant_id",
	"resource_type", "resource_id", "trace_id", "service", "reason",
}

// exportRow 把事件转换为 CSV 行；attrs 不导出，避免二次扩散敏感字段。
func exportRow(event audit.Event) []string {
	return []string{
		event.EventID,
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
		event.Action,
		string(event.Outcome),
		event.Channel,
		event.ActorID,
		event.ActorType,
		event.MerchantID,
		event.ResourceType,
		event.ResourceID,
		event.TraceID,
		event.Service,
		event.Reason,
	}
}

// ErrExportTooLarge 表示导出结果超过 audit.export.max_rows。
//
// 单独定义哨兵错误：同步导出要把它映射成 400（要求缩小时间范围），
// 异步任务要把它当成「不可重试的终态失败」——重试一百次也还是超限。
var ErrExportTooLarge = errors.New("audit export exceeds row limit")

// writeEventsCSV 把给定事件写成完整 CSV（表头 + 行）。
//
// 保留期归档复用同一份列定义：归档文件与用户导出必须列序一致，
// 否则事后核对「销毁前的数据」要比对两套口径。
func writeEventsCSV(w io.Writer, events []audit.Event) error {
	if w == nil {
		return apperr.Internal("audit archive requires a writer", nil)
	}
	writer := csv.NewWriter(w)
	if err := writer.Write(exportHeader); err != nil {
		return apperr.Internal("write audit archive header", err)
	}
	for _, event := range events {
		if err := writer.Write(exportRow(event)); err != nil {
			return apperr.Internal("write audit archive row", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return apperr.Internal("flush audit archive", err)
	}
	return nil
}

// writeCSV 把事件按固定列写入 w，返回写出的行数。
//
// 同步导出与异步任务共用本函数：两处一旦各写一份，列顺序与脱敏口径必然漂移，
// 同一份数据从两个入口拿到不同字段是合规事故而不是 bug。
//
// maxRows 为硬上限：超出即返回错误而不是截断，避免使用者以为拿到了全量。
func writeCSV(ctx context.Context, reader domain.Reader, filter domain.Filter, maxRows int, w io.Writer) (int, error) {
	if w == nil {
		return 0, apperr.Internal("audit export requires a writer", nil)
	}
	if maxRows <= 0 {
		return 0, apperr.Internal("audit export max rows is not configured", nil)
	}
	writer := csv.NewWriter(w)
	if err := writer.Write(exportHeader); err != nil {
		return 0, apperr.Internal("write audit export header", err)
	}
	rows := 0
	streamErr := reader.Stream(ctx, filter, func(event audit.Event) error {
		if rows >= maxRows {
			return fmt.Errorf("%w: export exceeds maximum of %d rows in the selected range",
				ErrExportTooLarge, maxRows)
		}
		if err := writer.Write(exportRow(event)); err != nil {
			return apperr.Internal("write audit export row", err)
		}
		rows++
		return nil
	})
	if streamErr != nil {
		return 0, streamErr
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, apperr.Internal("flush audit export", err)
	}
	return rows, nil
}

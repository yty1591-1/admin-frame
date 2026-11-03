package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// RetentionArchiver 把一批到期事件写成不可变归档对象。
//
// 返回对象键便于日志与元审计引用；实现必须保证「返回 nil 才算归档成功」，
// 保留期服务据此决定能否删除这批行——归档失败时宁可留数据，也不能裸删。
type RetentionArchiver interface {
	Archive(ctx context.Context, events []audit.Event, cutoff time.Time) (string, error)
}

// ObjectArchiver 把到期事件按 CSV 写入对象存储（AUD-07 的「到期归档销毁」路径）。
//
// 一个批次一个对象，键含日期与截止时间，便于按前缀做生命周期与容量治理；
// 对象内容与用户导出同列序（见 writeEventsCSV）。
type ObjectArchiver struct {
	// Objects 目标对象存储，必填。
	Objects storage.Store
	// Prefix 对象键前缀，如 audit/expired。
	Prefix string
	// Now 时钟；nil 时使用 time.Now，便于测试固定时间。
	Now func() time.Time
}

// Archive 生成并上传一个归档对象，返回对象键。
func (a ObjectArchiver) Archive(ctx context.Context, events []audit.Event, cutoff time.Time) (string, error) {
	if a.Objects == nil {
		return "", fmt.Errorf("audit archiver requires an object store")
	}
	if len(events) == 0 {
		return "", nil
	}
	payload := &bytes.Buffer{}
	if err := writeEventsCSV(payload, events); err != nil {
		return "", err
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	key := fmt.Sprintf("%s/%s/expired-%s-%d.csv",
		strings.Trim(strings.TrimSpace(a.Prefix), "/"),
		now().UTC().Format("2006/01/02"),
		cutoff.UTC().Format("20060102T150405Z"),
		now().UTC().UnixNano(),
	)
	if strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("audit archive prefix must be relative")
	}
	// 必须用 bytes.Reader 而不是 bytes.Buffer：S3 客户端在非 TLS 下要求可定位
	// 的流来算校验和，*bytes.Buffer 不实现 io.Seeker，PutObject 会直接失败
	// （导出曾用 bytes.NewReader 才没踩到）。
	if _, err := a.Objects.Put(ctx, key, bytes.NewReader(payload.Bytes()), storage.PutOptions{
		ContentType: "text/csv; charset=utf-8",
		Metadata: map[string]string{
			"kind":   "audit-retention-archive",
			"cutoff": cutoff.UTC().Format(time.RFC3339),
			"rows":   fmt.Sprintf("%d", len(events)),
		},
	}); err != nil {
		return "", fmt.Errorf("upload audit archive: %w", err)
	}
	return key, nil
}

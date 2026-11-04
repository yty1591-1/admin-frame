package domain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 导出 manifest 领域错误。
var (
	// ErrManifestTenantRequired 租户 ID 必填。
	ErrManifestTenantRequired = errors.New("tenant: export manifest tenant id is required")
	// ErrManifestSHAInvalid manifest SHA-256 必须是 64 位十六进制。
	ErrManifestSHAInvalid = errors.New("tenant: export manifest sha256 must be 64 hex characters")
	// ErrManifestCreatorRequired 登记操作者必填（A 端主体或 worker 系统名）。
	ErrManifestCreatorRequired = errors.New("tenant: export manifest creator is required")
	// ErrManifestNotFound 该租户没有已校验的导出 manifest。
	ErrManifestNotFound = errors.New("tenant: export manifest not found")
)

// manifestSHAPattern 校验 SHA-256 十六进制形态。
var manifestSHAPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ExportManifest 是一次「已校验」导出包的证据记录（D-05 第 2 段）。
//
// 登记即声明：导出包含 file_count 个文件、total_rows 行，清单文件哈希为
// manifest_sha256 且校验通过。硬删除任务以此作为前置证据——没有 manifest
// 的租户不允许进入硬删除（tenant-data-lifecycle.md §到期流程第 2 步）。
type ExportManifest struct {
	// TenantID 租户 ID（隔离边界）。
	TenantID string
	// ManifestSHA256 清单文件 SHA-256（64 位十六进制小写）。
	ManifestSHA256 string
	// FileCount 导出包含文件数。
	FileCount int
	// TotalRows 导出包含总行数。
	TotalRows int64
	// AcknowledgedAt 客户签收时间；零值表示未签收（导出窗口逾期视为放弃，
	// 不影响硬删除资格——放弃的是数据，不是处置义务）。
	AcknowledgedAt time.Time
	// CreatedBy 登记操作者（A 端主体或 worker 系统名）。
	CreatedBy string
	// CreatedAt 登记时间。
	CreatedAt time.Time
}

// Validate 登记合法性：SHA 形态、租户与操作者必填。
func (m ExportManifest) Validate() error {
	if strings.TrimSpace(m.TenantID) == "" {
		return ErrManifestTenantRequired
	}
	if !manifestSHAPattern.MatchString(strings.ToLower(m.ManifestSHA256)) {
		return ErrManifestSHAInvalid
	}
	if strings.TrimSpace(m.CreatedBy) == "" {
		return ErrManifestCreatorRequired
	}
	return nil
}

// Normalized 归一化副本（SHA 转小写、去空白）。
func (m ExportManifest) Normalized() ExportManifest {
	out := m
	out.TenantID = strings.TrimSpace(m.TenantID)
	out.ManifestSHA256 = strings.ToLower(strings.TrimSpace(m.ManifestSHA256))
	out.CreatedBy = strings.TrimSpace(m.CreatedBy)
	return out
}

// String 人类可读形态（日志与报告用）。
func (m ExportManifest) String() string {
	return fmt.Sprintf("manifest(tenant=%s sha=%s files=%d rows=%d)", m.TenantID, m.ManifestSHA256, m.FileCount, m.TotalRows)
}

// ExportManifestRepository 是导出证据的读写端口。
//
// 由租户域定义（消费方接口），MySQL 实现落 tenant_export_manifest 表；
// 处置任务（PurgeService）经 ExportEvidence 端口消费，业务域不直接依赖。
type ExportManifestRepository interface {
	// Record 登记一条已校验的导出 manifest。
	Record(ctx context.Context, m ExportManifest) error
	// FindLatest 返回该租户最新一条 manifest；不存在返回 ErrManifestNotFound。
	FindLatest(ctx context.Context, tenantID string) (ExportManifest, error)
}

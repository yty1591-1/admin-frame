package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// Snapshot 是配置中心某 env + namespace 的当前生效快照。
type Snapshot struct {
	// ReleaseID 是 config_release_pointer.active_release_id。
	ReleaseID int64
	// Version 是指针 CAS 版本，用于判断收敛。
	Version int64
	// Values 是生效键值；敏感项永远不会出现在这里（M2 走密钥解析）。
	Values map[string]string
}

// SnapshotProvider 提供生效快照；M1 由 MySQL 读路径实现，M1-5 由 SDK 实现。
type SnapshotProvider interface {
	Snapshot(ctx context.Context, env, namespace string) (Snapshot, error)
}

// DualReadDiff 是单键差异；只记录摘要，不记录原始值（避免日志泄露）。
type DualReadDiff struct {
	Key          string
	BaselineHash string
	CenterHash   string
}

// DualReadResult 是双读结果：Values 为最终取值（配置中心优先）。
type DualReadResult struct {
	ReleaseID  int64
	Version    int64
	Values     map[string]string
	Mismatches []DualReadDiff
	// MissingInCenter 是基线有、中心无的键（迁移未覆盖）。
	MissingInCenter []string
	// MissingInBaseline 是中心有、基线无的键（新增键）。
	MissingInBaseline []string
	// Degraded 为 true 表示中心不可用，Values 全部来自基线兜底。
	Degraded bool
}

// HasMismatch 判断是否存在值差异；迁移切换门禁要求为 false。
func (r DualReadResult) HasMismatch() bool {
	return len(r.Mismatches) > 0
}

// DualReadReporter 上报双读差异（指标 / 日志由调用方实现，nil 安全）。
type DualReadReporter interface {
	ReportDualRead(ctx context.Context, env, namespace string, result DualReadResult)
}

// DualReader 实现 CC-F-42：同进程同时读文件基线与配置中心，差异打点，
// 取值以配置中心为准；中心不可用时按 CFG-12 继续用基线兜底。
type DualReader struct {
	provider SnapshotProvider
	reporter DualReadReporter
}

// NewDualReader 构造双读解析器；provider 必填。
func NewDualReader(provider SnapshotProvider, reporter DualReadReporter) (*DualReader, error) {
	if provider == nil {
		return nil, apperr.Internal("dual reader requires a snapshot provider", nil)
	}
	return &DualReader{provider: provider, reporter: reporter}, nil
}

// Resolve 解析一份取值：中心优先、基线兜底，并上报差异。
//
// 返回错误仅表示中心不可用（已降级为基线取值）；调用方应继续服务并记录
// Degraded 指标，而不是阻断启动。
func (d *DualReader) Resolve(ctx context.Context, env, namespace string,
	baseline map[string]string) (DualReadResult, error) {
	if !domain.ValidEnv(env) || !domain.ValidNamespace(namespace) {
		return DualReadResult{}, apperr.BadRequest("invalid env or namespace")
	}
	snapshot, err := d.provider.Snapshot(ctx, env, namespace)
	if err != nil {
		result := DualReadResult{Values: copyValues(baseline), Degraded: true}
		d.report(ctx, env, namespace, result)
		return result, apperr.Unavailable(
			fmt.Sprintf("config center snapshot for %s/%s is unavailable; serving baseline", env, namespace))
	}

	result := DualReadResult{
		ReleaseID: snapshot.ReleaseID,
		Version:   snapshot.Version,
		Values:    copyValues(baseline),
	}
	for key, value := range snapshot.Values {
		result.Values[key] = value
	}
	for key, baselineValue := range baseline {
		centerValue, ok := snapshot.Values[key]
		if !ok {
			result.MissingInCenter = append(result.MissingInCenter, key)
			continue
		}
		if centerValue != baselineValue {
			result.Mismatches = append(result.Mismatches, DualReadDiff{
				Key:          key,
				BaselineHash: domain.HashValue(baselineValue),
				CenterHash:   domain.HashValue(centerValue),
			})
		}
	}
	for key := range snapshot.Values {
		if _, ok := baseline[key]; !ok {
			result.MissingInBaseline = append(result.MissingInBaseline, key)
		}
	}
	sort.Strings(result.MissingInCenter)
	sort.Strings(result.MissingInBaseline)
	sort.Slice(result.Mismatches, func(i, j int) bool {
		return result.Mismatches[i].Key < result.Mismatches[j].Key
	})
	d.report(ctx, env, namespace, result)
	return result, nil
}

// report 上报结果；上报失败不影响取值（指标链路不得阻断业务）。
func (d *DualReader) report(ctx context.Context, env, namespace string, result DualReadResult) {
	if d.reporter == nil {
		return
	}
	d.reporter.ReportDualRead(ctx, env, namespace, result)
}

// copyValues 深拷贝基线，避免调用方后续修改影响结果。
func copyValues(values map[string]string) map[string]string {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

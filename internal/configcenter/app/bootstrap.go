package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// BootstrapService 负责首次启动默认值播种与 spec 增量补种（CONFIG_STANDARDS §4 / CC-R-13）。
//
// 空 namespace：按代码 key spec 生成 baseline Release 并直接生效。
// 已有指针：仅补齐「有默认值且尚未进生效快照」的声明键，生成 reconcile
// Release 并切换指针；普通路径不覆盖已有键值。
// Repair=true 且 Overrides 含该键时，写新 ItemVersion 并发布 supersede Release（ADR-0018）。
type BootstrapService struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
	specs    *spec.Registry
	audit    auditContext
	ids      id.Generator
	now      func() time.Time
}

// NewBootstrapService 构造播种用例；仓储、key spec 注册表、审计出口与 ID 生成器必填。
func NewBootstrapService(items domain.ItemRepository, releases domain.ReleaseRepository,
	pointers domain.PointerRepository, specs *spec.Registry,
	emitter audit.Emitter, ids id.Generator, opts Options) (*BootstrapService, error) {
	switch {
	case items == nil:
		return nil, apperr.Internal("bootstrap service requires an item repository", nil)
	case releases == nil:
		return nil, apperr.Internal("bootstrap service requires a release repository", nil)
	case pointers == nil:
		return nil, apperr.Internal("bootstrap service requires a pointer repository", nil)
	case specs == nil:
		return nil, apperr.Internal("bootstrap service requires a key spec registry", nil)
	case emitter == nil:
		return nil, apperr.Internal("bootstrap service requires an audit emitter", nil)
	case ids == nil:
		return nil, apperr.Internal("bootstrap service requires an id generator", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &BootstrapService{
		items:    items,
		releases: releases,
		pointers: pointers,
		specs:    specs,
		audit:    auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:      ids,
		now:      now,
	}, nil
}

// BootstrapInput 是播种入参；调用方须包事务，保证初始化失败不留半状态。
type BootstrapInput struct {
	Env       string
	Namespace string
	Actor     Actor
	// Overrides 优先于 prod-like overlay 与 KeySpec.Default（安装向导提交值）。
	Overrides map[string]string
	// Repair 为真表示本轮是安装向导的 repair 轮次：Release 与审计标记为 install-repair。
	Repair bool
}

// BootstrapResult 是播种结果；SkippedKeys 不含值，仅供日志与运维核对。
type BootstrapResult struct {
	Seeded      bool
	ItemCount   int
	ReleaseID   int64
	ReleaseNo   int64
	SkippedKeys []string
}

// EnsureBaseline 在空 namespace 播种代码默认值；已有指针时增量补齐缺失声明键（CC-R-13）。
//
// 幂等：无缺失键时不新增 Release、不覆盖已有值。
// 敏感键与解析后仍无取值的键不播种，登记到 SkippedKeys。
// 默认值按环境经 spec.ResolveDefault 解析（CONFIG_STANDARDS §4）。
func (s *BootstrapService) EnsureBaseline(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	if err := requireActor(in.Actor); err != nil {
		return BootstrapResult{}, err
	}
	if !domain.ValidEnv(in.Env) || !domain.ValidNamespace(in.Namespace) {
		return BootstrapResult{}, apperr.BadRequest("invalid env or namespace")
	}
	specs := s.specs.List(in.Namespace)
	if len(specs) == 0 {
		return BootstrapResult{}, apperr.BadRequest(fmt.Sprintf("namespace %q has no declared key spec", in.Namespace))
	}

	pointer, err := s.pointers.Get(ctx, in.Env, in.Namespace)
	switch {
	case err == nil:
		return s.reconcileMissingDefaults(ctx, in, pointer, specs)
	case !errors.Is(err, domain.ErrNotFound):
		return BootstrapResult{}, translate(err)
	}

	now := s.now().UTC()
	result := BootstrapResult{}
	entries := make([]domain.ReleaseItem, 0, len(specs))
	for _, keySpec := range specs {
		seedValue := resolveSeedValue(in.Env, keySpec, in.Overrides)
		if keySpec.Sensitive || seedValue == "" {
			// 敏感值走 config_secret（M2）；无解析取值无法自动播种。
			result.SkippedKeys = append(result.SkippedKeys, keySpec.Key)
			continue
		}
		item, version, buildErr := s.buildSeededItem(keySpec, in, seedValue, now)
		if buildErr != nil {
			return BootstrapResult{}, buildErr
		}
		if createErr := s.items.Create(ctx, item); createErr != nil {
			return BootstrapResult{}, translate(createErr)
		}
		if appendErr := s.items.AppendVersion(ctx, version); appendErr != nil {
			return BootstrapResult{}, translate(appendErr)
		}
		entries = append(entries, domain.ReleaseItem{
			ItemID:      item.ID,
			ItemVersion: version.Version,
			ConfigKey:   item.Key,
			ValueHash:   version.ValueHash,
		})
	}
	if len(entries) == 0 {
		return result, nil
	}

	release, err := s.buildSeedRelease(ctx, in, entries, now, seedReleaseName(in, true),
		seedReleaseDescription(in, true))
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := s.releases.Create(ctx, release, entries); err != nil {
		return BootstrapResult{}, translate(err)
	}
	pointer = domain.Pointer{
		Env:             in.Env,
		Namespace:       in.Namespace,
		ActiveReleaseID: release.ID,
		Version:         1,
		UpdatedAt:       now,
	}
	if err := s.pointers.Create(ctx, pointer); err != nil {
		return BootstrapResult{}, translate(err)
	}

	if err := s.emitBootstrapAudit(ctx, in, release, len(entries), result.SkippedKeys,
		seedAuditMode(in, "bootstrap")); err != nil {
		return BootstrapResult{}, err
	}

	result.Seeded = true
	result.ItemCount = len(entries)
	result.ReleaseID = release.ID
	result.ReleaseNo = release.ReleaseNo
	return result, nil
}

// reconcileMissingDefaults 在已有生效快照上补齐新增的声明默认键。
func (s *BootstrapService) reconcileMissingDefaults(ctx context.Context, in BootstrapInput,
	pointer domain.Pointer, specs []domain.KeySpec) (BootstrapResult, error) {
	activeItems, err := s.releases.ListItems(ctx, pointer.ActiveReleaseID)
	if err != nil {
		return BootstrapResult{}, translate(err)
	}
	inRelease := make(map[string]domain.ReleaseItem, len(activeItems))
	for _, entry := range activeItems {
		inRelease[entry.ConfigKey] = entry
	}

	now := s.now().UTC()
	result := BootstrapResult{
		ReleaseID: pointer.ActiveReleaseID,
		ReleaseNo: pointer.Version,
	}
	added := make([]domain.ReleaseItem, 0)
	replaced := make(map[string]domain.ReleaseItem)
	for _, keySpec := range specs {
		seedValue := resolveSeedValue(in.Env, keySpec, in.Overrides)
		if keySpec.Sensitive || seedValue == "" {
			if _, ok := inRelease[keySpec.Key]; !ok {
				result.SkippedKeys = append(result.SkippedKeys, keySpec.Key)
			}
			continue
		}
		_, exists := inRelease[keySpec.Key]
		_, hasOverride := in.Overrides[keySpec.Key]
		// 普通 reconcile 不覆盖已有键；repair 轮次对 Overrides 内的键写新版本（ADR-0018）。
		if exists && !(in.Repair && hasOverride && strings.TrimSpace(in.Overrides[keySpec.Key]) != "") {
			continue
		}
		item, getErr := s.items.GetByKey(ctx, in.Env, in.Namespace, keySpec.Key)
		switch {
		case getErr == nil && exists && in.Repair && hasOverride:
			nextVersion := item.CurrentVersion + 1
			version, buildErr := buildVersion(s.ids, item, nextVersion, seedValue,
				"install-repair", in.Actor.SubjectID, now)
			if buildErr != nil {
				return BootstrapResult{}, buildErr
			}
			if appendErr := s.items.AppendVersion(ctx, version); appendErr != nil {
				return BootstrapResult{}, translate(appendErr)
			}
			updated := item
			updated.CurrentVersion = nextVersion
			updated.Version = item.Version + 1
			updated.UpdatedBy = in.Actor.SubjectID
			updated.UpdatedAt = now
			if uerr := s.items.UpdateCAS(ctx, updated, item.Version); uerr != nil {
				return BootstrapResult{}, translate(uerr)
			}
			replaced[keySpec.Key] = domain.ReleaseItem{
				ItemID: item.ID, ItemVersion: nextVersion,
				ConfigKey: item.Key, ValueHash: version.ValueHash,
			}
			continue
		case getErr == nil:
			// 键已存在但未进生效快照：按当前版本并入。
		case errors.Is(getErr, domain.ErrNotFound):
			created, version, buildErr := s.buildSeededItem(keySpec, in, seedValue, now)
			if buildErr != nil {
				return BootstrapResult{}, buildErr
			}
			if createErr := s.items.Create(ctx, created); createErr != nil {
				return BootstrapResult{}, translate(createErr)
			}
			if appendErr := s.items.AppendVersion(ctx, version); appendErr != nil {
				return BootstrapResult{}, translate(appendErr)
			}
			item = created
		default:
			return BootstrapResult{}, translate(getErr)
		}
		version, verErr := s.items.GetVersion(ctx, item.ID, item.CurrentVersion)
		if verErr != nil {
			return BootstrapResult{}, translate(verErr)
		}
		added = append(added, domain.ReleaseItem{
			ItemID: item.ID, ItemVersion: version.Version,
			ConfigKey: item.Key, ValueHash: version.ValueHash,
		})
	}
	if len(added) == 0 && len(replaced) == 0 {
		return result, nil
	}

	merged := make([]domain.ReleaseItem, 0, len(activeItems)+len(added))
	for _, entry := range activeItems {
		if repl, ok := replaced[entry.ConfigKey]; ok {
			merged = append(merged, repl)
			continue
		}
		merged = append(merged, entry)
	}
	merged = append(merged, added...)
	itemCount := len(added) + len(replaced)

	release, err := s.buildSeedRelease(ctx, in, merged, now, seedReleaseName(in, false),
		seedReleaseDescription(in, false))
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := s.releases.Create(ctx, release, merged); err != nil {
		return BootstrapResult{}, translate(err)
	}
	if err := s.releases.Transition(ctx, pointer.ActiveReleaseID,
		domain.ReleaseStatusActive, domain.ReleaseStatusSuperseded, in.Actor.SubjectID, now); err != nil {
		return BootstrapResult{}, translate(err)
	}
	if _, err := s.pointers.CompareAndSwap(ctx, in.Env, in.Namespace,
		pointer.Version, release.ID, now); err != nil {
		return BootstrapResult{}, translate(err)
	}
	if err := s.emitBootstrapAudit(ctx, in, release, itemCount, result.SkippedKeys,
		seedAuditMode(in, "reconcile")); err != nil {
		return BootstrapResult{}, err
	}

	result.Seeded = true
	result.ItemCount = itemCount
	result.ReleaseID = release.ID
	result.ReleaseNo = release.ReleaseNo
	return result, nil
}

func (s *BootstrapService) emitBootstrapAudit(ctx context.Context, in BootstrapInput,
	release domain.Release, itemCount int, skipped []string, mode string) error {
	attrs := map[string]string{
		"env":           in.Env,
		"namespace":     in.Namespace,
		"release_id":    fmt.Sprintf("%d", release.ID),
		"release_no":    fmt.Sprintf("%d", release.ReleaseNo),
		"item_count":    fmt.Sprintf("%d", itemCount),
		"skipped_count": fmt.Sprintf("%d", len(skipped)),
		"mode":          mode,
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigBootstrapCompleted,
		ResourceTypeRelease, releaseResourceID(release), audit.OutcomeSuccess, mode, attrs); err != nil {
		return apperr.Internal("emit bootstrap audit failed", err)
	}
	return nil
}

// buildSeededItem 按声明与解析后的取值生成一条 active 配置键与版本 1。
func (s *BootstrapService) buildSeededItem(keySpec domain.KeySpec, in BootstrapInput,
	seedValue string, now time.Time) (domain.Item, domain.ItemVersion, error) {
	if err := keySpec.ValidateValue(seedValue); err != nil {
		return domain.Item{}, domain.ItemVersion{}, apperr.BadRequest(
			fmt.Sprintf("seed value for %s: %v", keySpec.Key, err))
	}
	itemID, err := s.ids.Next()
	if err != nil {
		return domain.Item{}, domain.ItemVersion{}, apperr.Internal("generate item id failed", err)
	}
	item := domain.Item{
		ID:             itemID,
		Env:            in.Env,
		Namespace:      in.Namespace,
		Key:            keySpec.Key,
		ValueType:      keySpec.ValueType,
		Description:    keySpec.Description,
		Owner:          keySpec.Owner,
		Status:         domain.ItemStatusActive,
		CurrentVersion: 1,
		Version:        1,
		Sensitive:      keySpec.Sensitive,
		ValidationJSON: keySpec.ValidationJSON(),
		CreatedBy:      in.Actor.SubjectID,
		UpdatedBy:      in.Actor.SubjectID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := item.Validate(); err != nil {
		return domain.Item{}, domain.ItemVersion{}, apperr.BadRequest(err.Error())
	}
	version, err := buildVersion(s.ids, item, 1, seedValue, "bootstrap", in.Actor.SubjectID, now)
	if err != nil {
		return domain.Item{}, domain.ItemVersion{}, err
	}
	return item, version, nil
}

// buildSeedRelease 生成直接生效的播种 / 补种 Release。
func (s *BootstrapService) buildSeedRelease(ctx context.Context, in BootstrapInput,
	entries []domain.ReleaseItem, now time.Time, name, description string) (domain.Release, error) {
	releaseID, err := s.ids.Next()
	if err != nil {
		return domain.Release{}, apperr.Internal("generate release id failed", err)
	}
	releaseNo, err := s.releases.NextReleaseNo(ctx, in.Env, in.Namespace)
	if err != nil {
		return domain.Release{}, translate(err)
	}
	release := domain.Release{
		ID:           releaseID,
		Env:          in.Env,
		Namespace:    in.Namespace,
		ReleaseNo:    releaseNo,
		Name:         name,
		Description:  description,
		Status:       domain.ReleaseStatusActive,
		Strategy:     domain.StrategyAll,
		SnapshotHash: SnapshotHash(entries),
		SnapshotSize: len(entries),
		CreatedBy:    in.Actor.SubjectID,
		ApprovedBy:   in.Actor.SubjectID,
		ApprovedAt:   &now,
		PublishedAt:  &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	for i := range entries {
		entries[i].ReleaseID = releaseID
	}
	if err := release.Validate(); err != nil {
		return domain.Release{}, apperr.BadRequest(err.Error())
	}
	signed, err := applySignature(nil, release)
	if err != nil {
		return domain.Release{}, err
	}
	return signed, nil
}

// RequiredGap 是「已声明必填、但尚未进入生效快照」的配置键。
type RequiredGap struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	// Reason 取 no_active_release（该域尚无生效发布）或 missing_in_snapshot。
	Reason string `json:"reason"`
}

// KeyRef 返回 namespace/key 形式，用作 repair 白名单元素与前端展示。
func (g RequiredGap) KeyRef() string { return g.Namespace + "/" + g.Key }

// seedReleaseName 返回播种 / 补种 Release 的名称。
func seedReleaseName(in BootstrapInput, baseline bool) string {
	switch {
	case in.Repair:
		return "install-repair"
	case baseline:
		return "baseline"
	default:
		return "spec-reconcile"
	}
}

// seedReleaseDescription 返回 Release 描述，便于发布列表区分来源。
func seedReleaseDescription(in BootstrapInput, baseline bool) string {
	switch {
	case in.Repair:
		return "安装向导 repair 补齐阻塞启动的必填键"
	case baseline:
		return "首次启动按代码 key spec / prod-like overlay 播种"
	default:
		return "按代码 key spec 增量补齐缺失默认键"
	}
}

// seedAuditMode 返回播种审计的 mode 标签。
func seedAuditMode(in BootstrapInput, fallback string) string {
	if in.Repair {
		return "repair"
	}
	return fallback
}

// MissingRequired 统计各 namespace 中缺失的必填键，供安装向导与启动降级判断。
//
// 只统计非敏感键：敏感键的取值走 config_secret（生成 + 授权），不会进入
// 配置快照，若一并统计会把每个环境都判成永久缺键。
//
// 另附对象存储条件必填（driver=s3/minio 时的 bucket/region），与进程侧
// configboot.MissingRequired 共用 spec.MissingStorageRefs，避免安装态与启动态分裂。
func (s *BootstrapService) MissingRequired(ctx context.Context, env string) ([]RequiredGap, error) {
	if !domain.ValidEnv(env) {
		return nil, apperr.BadRequest("invalid env")
	}
	gaps := make([]RequiredGap, 0, 4)
	for _, namespace := range s.specs.Namespaces() {
		required := make([]domain.KeySpec, 0, 4)
		for _, keySpec := range s.specs.List(namespace) {
			if keySpec.Required && !keySpec.Sensitive {
				required = append(required, keySpec)
			}
		}
		if len(required) == 0 {
			continue
		}
		present, hasRelease, err := s.activeKeys(ctx, env, namespace)
		if err != nil {
			return nil, err
		}
		if !hasRelease {
			for _, keySpec := range required {
				gaps = append(gaps, RequiredGap{
					Namespace: namespace, Key: keySpec.Key, Reason: "no_active_release",
				})
			}
			continue
		}
		for _, keySpec := range required {
			if _, ok := present[keySpec.Key]; !ok {
				gaps = append(gaps, RequiredGap{
					Namespace: namespace, Key: keySpec.Key, Reason: "missing_in_snapshot",
				})
			}
		}
	}
	storageGaps, err := s.missingStorageGaps(ctx, env)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, storageGaps...)
	infraGaps, err := s.missingInfraGaps(ctx, env)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, infraGaps...)
	return gaps, nil
}

// missingStorageGaps 把 s3/minio 条件下仍缺的 bucket/region 记为安装阻塞键。
func (s *BootstrapService) missingStorageGaps(ctx context.Context, env string) ([]RequiredGap, error) {
	values, hasRelease, err := s.activeValues(ctx, env, spec.NamespacePlatform)
	if err != nil {
		return nil, err
	}
	if !hasRelease {
		return nil, nil
	}
	return refsToGaps(spec.MissingStorageRefs(values), spec.GapReasonMissingInSnapshot), nil
}

// missingInfraGaps 把 SMTP 等条件必填仍缺的非敏感键记为安装阻塞键。
func (s *BootstrapService) missingInfraGaps(ctx context.Context, env string) ([]RequiredGap, error) {
	merged := make(map[string]string)
	for _, namespace := range []string{spec.NamespacePlatform, spec.NamespaceNotification} {
		values, hasRelease, err := s.activeValues(ctx, env, namespace)
		if err != nil {
			return nil, err
		}
		if !hasRelease {
			continue
		}
		for key, value := range values {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return refsToGaps(spec.MissingInfraRefs(merged), spec.GapReasonMissingInSnapshot), nil
}

// refsToGaps 把 namespace/key 列表转成 RequiredGap。
func refsToGaps(refs []string, reason string) []RequiredGap {
	out := make([]RequiredGap, 0, len(refs))
	for _, ref := range refs {
		namespace, key, ok := strings.Cut(ref, "/")
		if !ok {
			continue
		}
		out = append(out, RequiredGap{
			Namespace: namespace, Key: key, Reason: reason,
		})
	}
	return out
}

// activeKeys 返回 namespace 生效发布内的键集合；hasRelease=false 表示该域尚无生效发布。
func (s *BootstrapService) activeKeys(ctx context.Context, env, namespace string) (
	present map[string]struct{}, hasRelease bool, err error) {
	values, hasRelease, err := s.activeValues(ctx, env, namespace)
	if err != nil || !hasRelease {
		return nil, hasRelease, err
	}
	present = make(map[string]struct{}, len(values))
	for key := range values {
		present[key] = struct{}{}
	}
	return present, true, nil
}

// MergedActiveValues 合并各 namespace 生效快照键值，供条件 Gaps / 连通性探测使用。
func (s *BootstrapService) MergedActiveValues(ctx context.Context, env string) (map[string]string, error) {
	merged := make(map[string]string)
	for _, namespace := range s.specs.Namespaces() {
		values, hasRelease, err := s.activeValues(ctx, env, namespace)
		if err != nil {
			return nil, err
		}
		if !hasRelease {
			continue
		}
		for key, value := range values {
			merged[key] = value
		}
	}
	return merged, nil
}

// activeValues 返回 namespace 生效发布的键值；hasRelease=false 表示该域尚无生效发布。
//
// 值为空串时仍会出现在 map 中（与「键不存在」可区分）；条件必填判定用 TrimSpace。
func (s *BootstrapService) activeValues(ctx context.Context, env, namespace string) (
	values map[string]string, hasRelease bool, err error) {
	pointer, err := s.pointers.Get(ctx, env, namespace)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, translate(err)
	}
	activeItems, err := s.releases.ListItems(ctx, pointer.ActiveReleaseID)
	if err != nil {
		return nil, false, translate(err)
	}
	values = make(map[string]string, len(activeItems))
	for _, entry := range activeItems {
		version, verErr := s.items.GetVersion(ctx, entry.ItemID, entry.ItemVersion)
		if verErr != nil {
			return nil, false, translate(verErr)
		}
		values[entry.ConfigKey] = version.ValueText
	}
	return values, true, nil
}

// CheckRefs 逐个判定 namespace/key 是否已进入生效快照。
//
// missing 是仍缺失的键；present 是已有值的键。begin(repair) 据此拒绝把
// 「已有值、需要走审批」的键塞进 repair 白名单（ADR-0018 待定 5）。
func (s *BootstrapService) CheckRefs(ctx context.Context, env string, refs []string) (
	missing []RequiredGap, present []string, err error) {
	if !domain.ValidEnv(env) {
		return nil, nil, apperr.BadRequest("invalid env")
	}
	cache := make(map[string]map[string]struct{}, 4)
	for _, ref := range refs {
		namespace, key, ok := strings.Cut(ref, "/")
		if !ok || namespace == "" || key == "" {
			return nil, nil, apperr.BadRequest(
				fmt.Sprintf("invalid key reference %q: want namespace/key", ref))
		}
		presentKeys, cached := cache[namespace]
		if !cached {
			presentKeys, _, err = s.activeKeys(ctx, env, namespace)
			if err != nil {
				return nil, nil, err
			}
			cache[namespace] = presentKeys
		}
		if _, ok := presentKeys[key]; ok {
			present = append(present, ref)
			continue
		}
		missing = append(missing, RequiredGap{
			Namespace: namespace, Key: key, Reason: "missing_in_snapshot",
		})
	}
	return missing, present, nil
}

// MissingRefs 只返回 refs 中仍缺失的键，供 apply / complete 的阻塞门禁复用。
func (s *BootstrapService) MissingRefs(ctx context.Context, env string, refs []string) ([]RequiredGap, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	missing, _, err := s.CheckRefs(ctx, env, refs)
	return missing, err
}

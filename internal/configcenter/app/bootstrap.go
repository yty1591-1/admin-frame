package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// BootstrapService 负责首次启动默认值播种（CONFIG_STANDARDS §4 / CC-F-44）。
//
// 触发条件：(env, namespace) 尚无生效指针。播种按代码 key spec 生成
// baseline Release 并直接生效；非空 namespace 不重复播种、不覆盖已有值。
type BootstrapService struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
	specs    *spec.Registry
	audit    auditContext
	ids      id.Generator
	now      func() time.Time
	signer   *configclient.Signer
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
		signer:   opts.Signer,
	}, nil
}

// BootstrapInput 是播种入参；调用方须包事务，保证初始化失败不留半状态。
type BootstrapInput struct {
	Env       string
	Namespace string
	Actor     Actor
}

// BootstrapResult 是播种结果；SkippedKeys 不含值，仅供日志与运维核对。
type BootstrapResult struct {
	Seeded      bool
	ItemCount   int
	ReleaseID   int64
	ReleaseNo   int64
	SkippedKeys []string
}

// EnsureBaseline 在空 namespace 播种代码默认值并生成生效的 baseline Release。
//
// 幂等：已存在生效指针时直接返回，不新增 Release、不覆盖已有值。
// 敏感键与无默认值的 required 键不播种，登记到 SkippedKeys。
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
		return BootstrapResult{ReleaseID: pointer.ActiveReleaseID, ReleaseNo: pointer.Version}, nil
	case !errors.Is(err, domain.ErrNotFound):
		return BootstrapResult{}, translate(err)
	}

	now := s.now().UTC()
	result := BootstrapResult{}
	entries := make([]domain.ReleaseItem, 0, len(specs))
	for _, keySpec := range specs {
		if keySpec.Sensitive || keySpec.Default == "" {
			// 敏感值走 config_secret（M2）；required 无默认值无法自动播种。
			result.SkippedKeys = append(result.SkippedKeys, keySpec.Key)
			continue
		}
		item, version, buildErr := s.buildSeededItem(keySpec, in, now)
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

	release, err := s.buildBaselineRelease(ctx, in, entries, now)
	if err != nil {
		return BootstrapResult{}, err
	}
	release, err = applySignature(s.signer, release)
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

	attrs := map[string]string{
		"env":           in.Env,
		"namespace":     in.Namespace,
		"release_id":    fmt.Sprintf("%d", release.ID),
		"release_no":    fmt.Sprintf("%d", release.ReleaseNo),
		"item_count":    fmt.Sprintf("%d", len(entries)),
		"skipped_count": fmt.Sprintf("%d", len(result.SkippedKeys)),
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigBootstrapCompleted,
		ResourceTypeRelease, releaseResourceID(release), audit.OutcomeSuccess, "bootstrap", attrs); err != nil {
		return BootstrapResult{}, apperr.Internal("emit bootstrap audit failed", err)
	}

	result.Seeded = true
	result.ItemCount = len(entries)
	result.ReleaseID = release.ID
	result.ReleaseNo = release.ReleaseNo
	return result, nil
}

// buildSeededItem 按声明生成一条 active 配置键与版本 1。
func (s *BootstrapService) buildSeededItem(keySpec domain.KeySpec, in BootstrapInput,
	now time.Time) (domain.Item, domain.ItemVersion, error) {
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
	version, err := buildVersion(s.ids, item, 1, keySpec.Default, "bootstrap", in.Actor.SubjectID, now)
	if err != nil {
		return domain.Item{}, domain.ItemVersion{}, err
	}
	return item, version, nil
}

// buildBaselineRelease 生成直接生效的 baseline Release。
func (s *BootstrapService) buildBaselineRelease(ctx context.Context, in BootstrapInput,
	entries []domain.ReleaseItem, now time.Time) (domain.Release, error) {
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
		Name:         "baseline",
		Description:  "首次启动按代码 key spec 默认值播种",
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
	return release, nil
}

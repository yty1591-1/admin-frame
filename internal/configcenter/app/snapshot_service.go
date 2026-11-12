package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// SnapshotService 组装某 env + namespace 的当前生效快照（含签名）。
//
// 供 pkg/configclient.Client 作为 Source 使用：只读、不做审批与发布判断。
// 敏感项只输出 value_hash，绝不把敏感值放进快照（CFG-12 / CC-F-54）。
type SnapshotService struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
}

// NewSnapshotService 构造快照读取服务；三个仓储必填。
func NewSnapshotService(items domain.ItemRepository, releases domain.ReleaseRepository,
	pointers domain.PointerRepository) (*SnapshotService, error) {
	switch {
	case items == nil:
		return nil, apperr.Internal("snapshot service requires an item repository", nil)
	case releases == nil:
		return nil, apperr.Internal("snapshot service requires a release repository", nil)
	case pointers == nil:
		return nil, apperr.Internal("snapshot service requires a pointer repository", nil)
	}
	return &SnapshotService{items: items, releases: releases, pointers: pointers}, nil
}

// Load 实现 configclient.Source。
func (s *SnapshotService) Load(ctx context.Context, env, namespace string) (configclient.Snapshot, error) {
	if !domain.ValidEnv(env) || !domain.ValidNamespace(namespace) {
		return configclient.Snapshot{}, apperr.BadRequest("invalid env or namespace")
	}
	pointer, err := s.pointers.Get(ctx, env, namespace)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return configclient.Snapshot{}, apperr.NotFound(
				fmt.Sprintf("config center has no active release for %s/%s", env, namespace))
		}
		return configclient.Snapshot{}, translate(err)
	}
	release, err := s.releases.Get(ctx, pointer.ActiveReleaseID)
	if err != nil {
		return configclient.Snapshot{}, translate(err)
	}
	items, err := s.releases.ListItems(ctx, release.ID)
	if err != nil {
		return configclient.Snapshot{}, translate(err)
	}

	snapshotItems := toSnapshotItems(items)
	if configclient.HashItems(snapshotItems) != release.SnapshotHash {
		return configclient.Snapshot{}, apperr.Internal("release snapshot hash mismatch", nil)
	}
	values := make(map[string]string, len(items))
	for _, releaseItem := range items {
		item, getErr := s.items.GetByID(ctx, releaseItem.ItemID)
		if getErr != nil {
			return configclient.Snapshot{}, translate(getErr)
		}
		if item.Sensitive {
			continue
		}
		version, versionErr := s.items.GetVersion(ctx, releaseItem.ItemID, releaseItem.ItemVersion)
		if versionErr != nil {
			return configclient.Snapshot{}, translate(versionErr)
		}
		values[releaseItem.ConfigKey] = version.ValueText
	}

	generatedAt := release.UpdatedAt
	if release.PublishedAt != nil {
		generatedAt = *release.PublishedAt
	}
	return configclient.Snapshot{
		Env:            release.Env,
		Namespace:      release.Namespace,
		ReleaseID:      release.ID,
		ReleaseNo:      release.ReleaseNo,
		PointerVersion: pointer.Version,
		SnapshotHash:   release.SnapshotHash,
		SnapshotSize:   release.SnapshotSize,
		KeyID:          release.SnapshotKeyID,
		Signature:      release.SnapshotSignature,
		Items:          snapshotItems,
		Values:         values,
		GeneratedAt:    generatedAt.UTC(),
	}, nil
}

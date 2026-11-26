package app

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// toSnapshotItems 把发布条目转换为快照契约中的指纹条目。
func toSnapshotItems(items []domain.ReleaseItem) []configclient.Item {
	converted := make([]configclient.Item, 0, len(items))
	for _, item := range items {
		converted = append(converted, configclient.Item{
			ConfigKey:   item.ConfigKey,
			ItemID:      item.ItemID,
			ItemVersion: item.ItemVersion,
			ValueHash:   item.ValueHash,
		})
	}
	return converted
}

// applySignature 已废弃签名（ADR-0016）；清空签名列并返回原 release。
func applySignature(_ any, release domain.Release) (domain.Release, error) {
	release.SnapshotKeyID = ""
	release.SnapshotSignature = ""
	return release, nil
}

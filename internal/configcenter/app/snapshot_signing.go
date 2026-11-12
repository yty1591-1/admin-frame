package app

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// toSnapshotItems 把发布条目转换为签名契约中的指纹条目。
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

// applySignature 对不可变快照签名并回填 key id / signature。
//
// signer 为 nil 表示本进程未配置签名密钥（local / 测试形态），保持空签名；
// 客户端会拒绝未签名快照并回退代码默认值，不会把未签名数据当作可信来源。
func applySignature(signer *configclient.Signer, release domain.Release) (domain.Release, error) {
	if signer == nil {
		return release, nil
	}
	keyID, signature, err := signer.Sign(configclient.Snapshot{
		Env:          release.Env,
		Namespace:    release.Namespace,
		ReleaseID:    release.ID,
		ReleaseNo:    release.ReleaseNo,
		SnapshotHash: release.SnapshotHash,
		SnapshotSize: release.SnapshotSize,
	})
	if err != nil {
		return domain.Release{}, apperr.Internal("sign config snapshot failed", err)
	}
	release.SnapshotKeyID = keyID
	release.SnapshotSignature = signature
	return release, nil
}

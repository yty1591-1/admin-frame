package configcenterruntime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// 本地签名快照是 M1 冷启动取值来源：部署把上一次成功发布的快照挂载到
// 进程内，验签通过后覆盖静态配置；缺失或验签失败由调用方降级。
const (
	snapshotFileEnv       = "CONFIGCENTER_SNAPSHOT_FILE"
	snapshotPublicKeysEnv = "CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS"
)

// StartupValues 读取并验签本地快照文件，返回扁平取值（yaml 路径 → 字符串）。
//
// 未配置 CONFIGCENTER_SNAPSHOT_FILE 时返回 (nil, nil)，表示不接入配置中心。
// 配置了文件但缺少可信公钥、文件损坏或验签失败返回错误；调用方应记录告警
// 并继续使用文件配置（降级），而不是把未验签数据当作可信来源。
func StartupValues(ctx context.Context) (map[string]string, error) {
	path := strings.TrimSpace(os.Getenv(snapshotFileEnv))
	if path == "" {
		return nil, nil
	}
	rawKeys := strings.TrimSpace(os.Getenv(snapshotPublicKeysEnv))
	if rawKeys == "" {
		return nil, fmt.Errorf("configcenter: %s is required when %s is set",
			snapshotPublicKeysEnv, snapshotFileEnv)
	}
	keys, err := configclient.ParsePublicKeys(rawKeys)
	if err != nil {
		return nil, err
	}
	verifier, err := configclient.NewVerifier(keys)
	if err != nil {
		return nil, err
	}
	store, err := configclient.NewFileStore(path)
	if err != nil {
		return nil, err
	}
	snapshot, err := configclient.LoadFromStore(ctx, store, verifier)
	if err != nil {
		return nil, err
	}
	return snapshot.Values, nil
}

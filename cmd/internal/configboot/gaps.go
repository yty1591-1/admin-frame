package configboot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// MissingRequired 返回启动 Values 中仍缺值的必填键（namespace/key 形式）。
//
// 判定口径与 config-api 的安装态 gaps 一致：只统计 Required 且非 Sensitive
// 的键——敏感键走 config_secret（缺失由密钥自动生成流程负责），若把它们算进
// 阻塞会把环境判成永久缺键（ADR-0018）。
//
// 必须在所有进程级补齐（SetIfEmpty / ApplyProdLikeProcessDefaults /
// ApplyDeployOverlays）之后调用：代码能补的键不算缺，否则会误降级。
func MissingRequired(values map[string]string) ([]string, error) {
	registry, err := spec.Platform()
	if err != nil {
		return nil, fmt.Errorf("configboot: load key spec: %w", err)
	}
	gaps := make([]string, 0, 4)
	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			if !keySpec.Required || keySpec.Sensitive {
				continue
			}
			if strings.TrimSpace(values[keySpec.Key]) == "" {
				gaps = append(gaps, ns+"/"+keySpec.Key)
			}
		}
	}
	sort.Strings(gaps)
	return gaps, nil
}

// RequireComplete 供 fail-fast 进程使用：仍缺必填键时返回带修复指引的启动错误。
//
// admin-api 不走本函数——它降级为 setup-only；其余进程无法自行修复，只能
// crash-loop 等待管理员通过 admin 安装向导补齐（ADR-0018 决策 2）。
func RequireComplete(service string, values map[string]string) error {
	gaps, err := MissingRequired(values)
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		return nil
	}
	return fmt.Errorf("%s: config incomplete: missing required keys: %s; run the admin install wizard to seed them",
		service, strings.Join(gaps, ", "))
}

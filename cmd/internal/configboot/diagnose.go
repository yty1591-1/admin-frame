package configboot

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// FillPlaceholders 用类型正确的占位值填满「已声明但为空」的非敏感键。
//
// 返回填充后的副本与被填充键（namespace/key，已排序）。敏感键不参与：其取值
// 来自 config_secret（生成 + 授权），不进入配置快照，占位也没有意义。
func FillPlaceholders(values map[string]string) (filled map[string]string, refs []string, err error) {
	registry, err := spec.Platform()
	if err != nil {
		return nil, nil, fmt.Errorf("config boot: registry: %w", err)
	}
	filled = make(map[string]string, len(values)+8)
	for key, value := range values {
		filled[key] = value
	}
	refs = make([]string, 0, 8)
	for _, namespace := range registry.Namespaces() {
		for _, keySpec := range registry.List(namespace) {
			if keySpec.Sensitive {
				continue
			}
			if strings.TrimSpace(filled[keySpec.Key]) != "" {
				continue
			}
			filled[keySpec.Key] = placeholderValue(keySpec)
			refs = append(refs, namespace+"/"+keySpec.Key)
		}
	}
	sort.Strings(refs)
	return filled, refs, nil
}

// DiagnoseMissingValues 判定启动失败是否由「缺值」造成（ADR-0018 待定 5）。
//
// 做法：给已声明但为空的键填上占位值后重跑一次校验。校验通过说明失败原因确实
// 是这些键为空，可以交给安装向导 repair 补齐；仍然失败说明还存在非法值，属配置
// 写错，不降级（ADR-0018 决策 3）。占位值本身不合法时会落到后者——方向偏保守：
// 宁可不降级，也不把带病进程挂在降级态。
func DiagnoseMissingValues(values map[string]string, validate func(map[string]string) error) ([]string, bool) {
	if validate == nil || len(values) == 0 {
		return nil, false
	}
	filled, refs, err := FillPlaceholders(values)
	if err != nil || len(refs) == 0 {
		return nil, false
	}
	if validate(filled) != nil {
		return nil, false
	}
	return refs, true
}

// placeholderValue 按键值类型构造占位值，枚举优先取首项。
func placeholderValue(keySpec domain.KeySpec) string {
	if len(keySpec.Enum) > 0 {
		return keySpec.Enum[0]
	}
	switch keySpec.ValueType {
	case domain.ValueTypeInt:
		if keySpec.MinInt != nil && *keySpec.MinInt > 1 {
			return strconv.FormatInt(*keySpec.MinInt, 10)
		}
		return "1"
	case domain.ValueTypeBool:
		return "true"
	case domain.ValueTypeDuration:
		return "1s"
	case domain.ValueTypeJSON:
		return "{}"
	default:
		return "placeholder"
	}
}

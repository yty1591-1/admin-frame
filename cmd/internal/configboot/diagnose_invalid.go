package configboot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// defaultCandidate 是一个「非敏感、带声明默认值」的键。
type defaultCandidate struct {
	Ref   string
	Key   string
	Value string
}

// defaultCandidates 返回可重置为默认值的键（Ref 为 namespace/key）。
func defaultCandidates() ([]defaultCandidate, error) {
	registry, err := spec.Platform()
	if err != nil {
		return nil, fmt.Errorf("configboot: load key spec: %w", err)
	}
	out := make([]defaultCandidate, 0, 64)
	for _, ns := range registry.Namespaces() {
		for _, keySpec := range registry.List(ns) {
			if keySpec.Sensitive || strings.TrimSpace(keySpec.Default) == "" {
				continue
			}
			out = append(out, defaultCandidate{
				Ref: ns + "/" + keySpec.Key, Key: keySpec.Key, Value: keySpec.Default,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// DiagnoseInvalidValues 判断启动校验失败是否「把若干键重置为声明默认值」即可通过。
//
// 只在已知校验失败后调用；返回的 refs 交给 config-api 重置。取舍是保守的：
// 先按错误文案里出现的键精确定位，再退回逐个单键试探；两次都判不出就返回
// false，调用方继续按「值非法」fail-fast，不做批量重置（避免顺手改掉无关键）。
func DiagnoseInvalidValues(values map[string]string, validate func(map[string]string) error) ([]string, bool) {
	if validate == nil {
		return nil, false
	}
	verr := validate(values)
	if verr == nil {
		return nil, false
	}
	candidates, err := defaultCandidates()
	if err != nil || len(candidates) == 0 {
		return nil, false
	}

	// 一阶段：按错误文案里点名的键精确定位。
	if named := namedCandidates(verr.Error(), candidates); len(named) > 0 {
		trial := copyValues(values)
		refs := make([]string, 0, len(named))
		for _, candidate := range named {
			if _, exists := trial[candidate.Key]; !exists {
				continue
			}
			trial[candidate.Key] = candidate.Value
			refs = append(refs, candidate.Ref)
		}
		if len(refs) > 0 && validate(trial) == nil {
			sort.Strings(refs)
			return refs, true
		}
	}

	// 二阶段：逐个单键试探，锁定唯一肇事键。
	for _, candidate := range candidates {
		if _, exists := values[candidate.Key]; !exists {
			continue
		}
		trial := copyValues(values)
		trial[candidate.Key] = candidate.Value
		if validate(trial) == nil {
			return []string{candidate.Ref}, true
		}
	}
	return nil, false
}

// namedCandidates 返回错误文案中出现过的候选键（保持 Ref 字典序）。
func namedCandidates(message string, candidates []defaultCandidate) []defaultCandidate {
	out := make([]defaultCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.Contains(message, candidate.Key) {
			out = append(out, candidate)
		}
	}
	return out
}

func copyValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values)+1)
	for key, value := range values {
		out[key] = value
	}
	return out
}

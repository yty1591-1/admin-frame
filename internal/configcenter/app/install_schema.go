package app

import (
	"context"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 安装向导的模式（ADR-0018）：ready 之前是首装，之后是补配/修复。
const (
	InstallModeFirstInstall = string(domain.InstallModeFirstInstall)
	InstallModeRepair       = string(domain.InstallModeRepair)
)

// 默认值来源：与 spec.DefaultSource* 同值，供 OpenAPI / 前端契约稳定引用。
const (
	DefaultSourceCode     = spec.DefaultSourceCode
	DefaultSourceProdLike = spec.DefaultSourceProd
	DefaultSourceDev      = spec.DefaultSourceDev
	DefaultSourceNone     = spec.DefaultSourceNone
)

// InstallSchema 是向导渲染表单所需的元数据（只出声明信息，不出运行值）。
type InstallSchema struct {
	Env           string            `json:"env"`
	Mode          string            `json:"mode"`
	Status        string            `json:"status"`
	BlockingCount int               `json:"blocking_count"`
	BlockingKeys  []SchemaGap       `json:"blocking_keys"`
	Namespaces    []SchemaNamespace `json:"namespaces"`
	Fields        []SchemaField     `json:"fields"`
}

// SchemaGap 是缺失的必填键。
type SchemaGap struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
}

// SchemaNamespace 是命名空间在表单里的展示信息与进度。
type SchemaNamespace struct {
	Namespace     string `json:"namespace"`
	Title         string `json:"title"`
	Order         int    `json:"order"`
	KeyCount      int    `json:"key_count"`
	BlockingCount int    `json:"blocking_count"`
}

// SchemaField 是单个配置键的渲染元数据。
type SchemaField struct {
	Namespace    string            `json:"namespace"`
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	Group        string            `json:"group"`
	GroupTitle   string            `json:"group_title"`
	Order        int               `json:"order"`
	Widget       string            `json:"widget"`
	ValueType    string            `json:"value_type"`
	Required     bool              `json:"required"`
	Sensitive    bool              `json:"sensitive"`
	Deprecated   bool              `json:"deprecated"`
	Blocking     bool              `json:"blocking"`
	ReloadMode   string            `json:"reload_mode"`
	HotWhitelist bool              `json:"hot_whitelist"`
	Enum         []string          `json:"enum,omitempty"`
	MinInt       *int64            `json:"min_int,omitempty"`
	MaxInt       *int64            `json:"max_int,omitempty"`
	Pattern      string            `json:"pattern,omitempty"`
	Description  string            `json:"description"`
	Owner        string            `json:"owner"`
	Default      SchemaDefault     `json:"default"`
	Secret       *SchemaSecretRule `json:"secret,omitempty"`
}

// SchemaDefault 描述默认取值与来源，前端据此显示「默认：xxx」徽标。
type SchemaDefault struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// SchemaSecretRule 是敏感键的生成与轮换规则（不含任何密钥值）。
type SchemaSecretRule struct {
	Algorithm    string `json:"algorithm"`
	AutoGenerate bool   `json:"auto_generate"`
	Encoding     string `json:"encoding"`
	MinLen       int    `json:"min_len"`
	MaxLen       int    `json:"max_len"`
	Prefix       string `json:"prefix,omitempty"`
}

// Schema 返回向导渲染元数据：字段清单 + 分组 + 缺失必填键 + 模式。
//
// 端点本身要求 setup token（见 transport），因为 schema 暴露键名与默认值。
func (s *InstallService) Schema(ctx context.Context, env string) (InstallSchema, error) {
	if !domain.ValidEnv(env) {
		return InstallSchema{}, apperr.BadRequest("invalid env")
	}
	state, err := s.Status(ctx, env)
	if err != nil {
		return InstallSchema{}, err
	}
	gaps, err := s.Gaps(ctx, env)
	if err != nil {
		return InstallSchema{}, err
	}

	out := InstallSchema{
		Env:          env,
		Mode:         InstallModeFirstInstall,
		Status:       string(state.Status),
		BlockingKeys: make([]SchemaGap, 0, len(gaps)),
	}
	if state.Status == domain.InstallReady || state.Status == domain.InstallFailed ||
		state.Mode == domain.InstallModeRepair {
		out.Mode = InstallModeRepair
	}
	blocking := make(map[string]map[string]struct{}, len(gaps))
	for _, gap := range gaps {
		out.BlockingKeys = append(out.BlockingKeys, SchemaGap{
			Namespace: gap.Namespace, Key: gap.Key, Label: gap.Key, Reason: gap.Reason,
		})
		if blocking[gap.Namespace] == nil {
			blocking[gap.Namespace] = make(map[string]struct{}, 2)
		}
		blocking[gap.Namespace][gap.Key] = struct{}{}
	}
	out.BlockingCount = len(out.BlockingKeys)

	hotSet := spec.HotKeySet()
	namespaces := s.specs.Namespaces()
	sort.Slice(namespaces, func(i, j int) bool {
		_, orderI := spec.NamespaceTitle(namespaces[i])
		_, orderJ := spec.NamespaceTitle(namespaces[j])
		if orderI != orderJ {
			return orderI < orderJ
		}
		return namespaces[i] < namespaces[j]
	})

	for _, namespace := range namespaces {
		title, order := spec.NamespaceTitle(namespace)
		listed := s.specs.List(namespace)
		nsView := SchemaNamespace{
			Namespace: namespace, Title: title, Order: order,
			KeyCount: len(listed), BlockingCount: len(blocking[namespace]),
		}
		out.Namespaces = append(out.Namespaces, nsView)

		groupIndex := make(map[string]int, len(listed))
		for _, keySpec := range listed {
			group := spec.GroupOf(keySpec.Key)
			index := groupIndex[group.ID]
			groupIndex[group.ID] = index + 1
			_, isHot := hotSet[keySpec.Key]
			isBlocking := hasKey(blocking[namespace], keySpec.Key)
			field := SchemaField{
				Namespace:  namespace,
				Key:        keySpec.Key,
				Label:      spec.DeriveLabel(keySpec),
				Group:      group.ID,
				GroupTitle: group.Title,
				Order:      fieldOrder(order, group.Order, index),
				Widget:     spec.WidgetFor(keySpec),
				ValueType:  string(keySpec.ValueType),
				// 阻塞键必然是「不填就装不完」的，即使它没有 Required 标记
				// （storage.s3.bucket 这类条件必填由进程侧补判），也要在
				// 向导里标成必填，否则前端提交时不会拦截空值。
				Required:     keySpec.Required || isBlocking,
				Sensitive:    keySpec.Sensitive,
				Deprecated:   keySpec.Deprecated,
				Blocking:     isBlocking,
				ReloadMode:   string(keySpec.EffectiveReloadMode()),
				HotWhitelist: isHot,
				Enum:         keySpec.Enum,
				MinInt:       keySpec.MinInt,
				MaxInt:       keySpec.MaxInt,
				Pattern:      keySpec.Pattern,
				Description:  keySpec.Description,
				Owner:        keySpec.Owner,
				Default:      schemaDefaultFor(env, keySpec),
			}
			if rule := keySpec.SecretRule; rule != nil {
				field.Secret = &SchemaSecretRule{
					Algorithm:    strings.TrimSpace(rule.Algorithm),
					AutoGenerate: rule.AutoGenerate,
					Encoding:     rule.Encoding,
					MinLen:       rule.MinLen,
					MaxLen:       rule.MaxLen,
					Prefix:       rule.Prefix,
				}
			}
			out.Fields = append(out.Fields, field)
		}
	}

	// 回填缺失键的短标签，便于前端直接渲染阻塞提示。
	labels := make(map[string]string, len(out.Fields))
	for _, field := range out.Fields {
		labels[field.Namespace+"\x00"+field.Key] = field.Label
	}
	for i := range out.BlockingKeys {
		if label, ok := labels[out.BlockingKeys[i].Namespace+"\x00"+out.BlockingKeys[i].Key]; ok {
			out.BlockingKeys[i].Label = label
		}
	}
	return out, nil
}

// schemaDefaultFor 解析默认值与来源（与播种 / 自愈同源：spec.ResolveDefault）。
func schemaDefaultFor(env string, keySpec domain.KeySpec) SchemaDefault {
	value, source := spec.ResolveDefault(env, keySpec)
	if source == spec.DefaultSourceNone {
		return SchemaDefault{Source: DefaultSourceNone}
	}
	return SchemaDefault{Value: value, Source: source}
}

// fieldOrder 把「命名空间顺序 + 分组顺序 + 组内序号」压成一个可比较整数。
func fieldOrder(namespaceOrder, groupOrder, index int) int {
	return (namespaceOrder*10000+groupOrder)*1000 + index
}

// hasKey 判断集合是否包含键。
func hasKey(set map[string]struct{}, key string) bool {
	if set == nil {
		return false
	}
	_, ok := set[key]
	return ok
}

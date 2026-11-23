package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// maxResetRefs 限制单次重置的键数，避免一次请求改写过多取值。
const maxResetRefs = 32

// resetReleaseName / resetReleaseDescription 标记自动恢复生成的发布。
const (
	resetReleaseName        = "install-reset-defaults"
	resetReleaseDescription = "启动阻塞：把取值非法的键重置为声明默认值"
	resetAuditMode          = "reset-defaults"
)

// ResetSkip 记录未被重置的键及原因。
type ResetSkip struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

// ResetEntry 记录一个被重置为默认值的键。
type ResetEntry struct {
	Namespace     string `json:"namespace"`
	Key           string `json:"key"`
	DefaultSource string `json:"default_source"`
}

// ResetResult 是重置结果；ReleaseKeys 按域给出生效指针的新键。
type ResetResult struct {
	Reset       []ResetEntry      `json:"reset"`
	Skipped     []ResetSkip       `json:"skipped"`
	ReleaseKeys map[string]string `json:"release_keys"`
}

// ResetInput 是「把取值非法的键重置为声明默认值」的入参。
//
// 服务端自行解析默认值（prod-like overlay > code default），调用方只声明键，
// 不能借这个接口注入任意取值。
type ResetInput struct {
	Env   string
	Refs  []string
	Actor Actor
}

// ResetToDefaults 把给定键重置为声明默认值并生成一个直接生效的发布。
//
// 仅用于「键存在但取值非法导致进程无法启动」的场景（ADR-0018 待定 1）：
// 取值为声明默认值、不接收调用方指定值，且逐个键留审计。
func (s *BootstrapService) ResetToDefaults(ctx context.Context, in ResetInput) (ResetResult, error) {
	result := ResetResult{ReleaseKeys: map[string]string{}}
	if err := requireActor(in.Actor); err != nil {
		return result, err
	}
	if !domain.ValidEnv(in.Env) {
		return result, apperr.BadRequest(fmt.Sprintf("invalid env %q", in.Env))
	}
	byNamespace, _, err := groupRefs(in.Refs)
	if err != nil {
		return result, err
	}
	for _, namespace := range sortedRefKeys(byNamespace) {
		if err := s.resetNamespace(ctx, in, namespace, byNamespace[namespace], &result); err != nil {
			return ResetResult{}, err
		}
	}
	sort.Slice(result.Reset, func(i, j int) bool {
		if result.Reset[i].Namespace != result.Reset[j].Namespace {
			return result.Reset[i].Namespace < result.Reset[j].Namespace
		}
		return result.Reset[i].Key < result.Reset[j].Key
	})
	return result, nil
}

// resetNamespace 在一个域内重置取值并生成新发布。
func (s *BootstrapService) resetNamespace(ctx context.Context, in ResetInput, namespace string,
	keys []string, result *ResetResult) error {
	pointer, err := s.pointers.Get(ctx, in.Env, namespace)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			for _, key := range keys {
				result.Skipped = append(result.Skipped, ResetSkip{Ref: namespace + "/" + key, Reason: "no_active_release"})
			}
			return nil
		}
		return translate(err)
	}
	activeItems, err := s.releases.ListItems(ctx, pointer.ActiveReleaseID)
	if err != nil {
		return translate(err)
	}
	entries := make(map[string]domain.ReleaseItem, len(activeItems))
	for _, entry := range activeItems {
		entries[entry.ConfigKey] = entry
	}

	now := s.now().UTC()
	bootstrap := BootstrapInput{Env: in.Env, Namespace: namespace, Actor: in.Actor}
	reset := 0
	for _, key := range keys {
		ref := namespace + "/" + key
		keySpec, ok := s.specs.Lookup(namespace, key)
		switch {
		case !ok:
			result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "unknown_key"})
			continue
		case keySpec.Sensitive:
			result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "sensitive_key"})
			continue
		}
		value, source := resetDefaultFor(in.Env, keySpec)
		if value == "" {
			result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "no_default"})
			continue
		}
		if verr := keySpec.ValidateValue(value); verr != nil {
			result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "default_invalid"})
			continue
		}
		item, gerr := s.items.GetByKey(ctx, in.Env, namespace, key)
		if gerr != nil {
			if errors.Is(gerr, domain.ErrNotFound) {
				result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "missing_item"})
				continue
			}
			return translate(gerr)
		}
		if item.Status == domain.ItemStatusArchived {
			result.Skipped = append(result.Skipped, ResetSkip{Ref: ref, Reason: "archived_item"})
			continue
		}
		nextVersion := item.CurrentVersion + 1
		version, berr := buildVersion(s.ids, item, nextVersion, value, "reset-to-default", in.Actor.SubjectID, now)
		if berr != nil {
			return berr
		}
		if apErr := s.items.AppendVersion(ctx, version); apErr != nil {
			return translate(apErr)
		}
		updated := item
		updated.CurrentVersion = nextVersion
		updated.Version = item.Version + 1
		updated.UpdatedBy = in.Actor.SubjectID
		updated.UpdatedAt = now
		if uerr := s.items.UpdateCAS(ctx, updated, item.Version); uerr != nil {
			return translate(uerr)
		}
		entries[key] = domain.ReleaseItem{
			ItemID: item.ID, ItemVersion: nextVersion,
			ConfigKey: key, ValueHash: version.ValueHash,
		}
		reset++
		result.Reset = append(result.Reset, ResetEntry{Namespace: namespace, Key: key, DefaultSource: source})
	}
	if reset == 0 {
		return nil
	}

	merged := make([]domain.ReleaseItem, 0, len(entries))
	for _, key := range sortedRefKeys(entries) {
		merged = append(merged, entries[key])
	}
	release, err := s.buildSeedRelease(ctx, bootstrap, merged, now, resetReleaseName, resetReleaseDescription)
	if err != nil {
		return err
	}
	if cerr := s.releases.Create(ctx, release, merged); cerr != nil {
		return translate(cerr)
	}
	if terr := s.releases.Transition(ctx, pointer.ActiveReleaseID,
		domain.ReleaseStatusActive, domain.ReleaseStatusSuperseded, in.Actor.SubjectID, now); terr != nil {
		return translate(terr)
	}
	next, serr := s.pointers.CompareAndSwap(ctx, in.Env, namespace,
		pointer.Version, release.ID, now)
	if serr != nil {
		return translate(serr)
	}
	if aerr := s.emitResetAudit(ctx, in, bootstrap, release, result.Reset); aerr != nil {
		return aerr
	}
	result.ReleaseKeys[namespace] = ReleaseKey(release.ID, next.Version)
	return nil
}

func (s *BootstrapService) emitResetAudit(ctx context.Context, in ResetInput, bootstrap BootstrapInput,
	release domain.Release, entries []ResetEntry) error {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Namespace == bootstrap.Namespace {
			keys = append(keys, entry.Key)
		}
	}
	attrs := map[string]string{
		"env":        in.Env,
		"namespace":  bootstrap.Namespace,
		"release_id": fmt.Sprintf("%d", release.ID),
		"release_no": fmt.Sprintf("%d", release.ReleaseNo),
		"keys":       strings.Join(keys, ","),
		"key_count":  fmt.Sprintf("%d", len(keys)),
		"mode":       resetAuditMode,
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigReleasePublish, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, resetAuditMode, attrs); err != nil {
		return apperr.Internal("emit reset audit failed", err)
	}
	return nil
}

// resetDefaultFor 解析重置取值：prod-like overlay 优先，其次代码声明默认值。
func resetDefaultFor(env string, keySpec domain.KeySpec) (value, source string) {
	if platformconfig.IsProdLikeEnv(env) {
		if overlay, ok := prodLikeSeedDefaults()[keySpec.Key]; ok && overlay != "" {
			return overlay, "prod_default"
		}
	}
	return keySpec.Default, "code_default"
}

// groupRefs 解析并校验 namespace/key 形式的引用，按域分组。
func groupRefs(input []string) (byNamespace map[string][]string, refs map[string]string, err error) {
	byNamespace = map[string][]string{}
	refs = map[string]string{}
	for _, raw := range input {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		namespace, key, ok := strings.Cut(ref, "/")
		if !ok || strings.TrimSpace(namespace) == "" || strings.TrimSpace(key) == "" {
			return nil, nil, apperr.BadRequest(fmt.Sprintf("invalid key reference %q: want namespace/key", ref))
		}
		if _, seen := refs[ref]; seen {
			continue
		}
		refs[ref] = namespace
		byNamespace[namespace] = append(byNamespace[namespace], key)
	}
	if len(refs) == 0 {
		return nil, nil, apperr.BadRequest("refs is required")
	}
	if len(refs) > maxResetRefs {
		return nil, nil, apperr.BadRequest(fmt.Sprintf("refs accepts at most %d keys", maxResetRefs))
	}
	for namespace := range byNamespace {
		sort.Strings(byNamespace[namespace])
	}
	return byNamespace, refs, nil
}

// sortedRefKeys 返回 map 的排序键，用于稳定遍历。
func sortedRefKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

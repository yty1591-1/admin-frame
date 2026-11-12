package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// ReleaseService 是发布用例：快照构建、送审、审批、生效、回滚与取消。
//
// 发布内容不可变：生效与回滚只切换 config_release_pointer；回滚按契约
// （§6.3）生成一个新 Release 指向历史快照，旧 active 转 rolled_back。
type ReleaseService struct {
	items    domain.ItemRepository
	releases domain.ReleaseRepository
	pointers domain.PointerRepository
	audit    auditContext
	ids      id.Generator
	now      func() time.Time
}

// NewReleaseService 构造发布用例；三个仓储、审计出口与 ID 生成器必填。
func NewReleaseService(items domain.ItemRepository, releases domain.ReleaseRepository,
	pointers domain.PointerRepository, emitter audit.Emitter, ids id.Generator,
	opts Options) (*ReleaseService, error) {
	switch {
	case items == nil:
		return nil, apperr.Internal("release service requires an item repository", nil)
	case releases == nil:
		return nil, apperr.Internal("release service requires a release repository", nil)
	case pointers == nil:
		return nil, apperr.Internal("release service requires a pointer repository", nil)
	case emitter == nil:
		return nil, apperr.Internal("release service requires an audit emitter", nil)
	case ids == nil:
		return nil, apperr.Internal("release service requires an id generator", nil)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ReleaseService{
		items:    items,
		releases: releases,
		pointers: pointers,
		audit:    auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now},
		ids:      ids,
		now:      now,
	}, nil
}

// SnapshotEntry 指定快照中某个配置键的版本。
type SnapshotEntry struct {
	ItemID      int64
	ItemVersion int64
}

// CreateReleaseInput 是创建发布单入参。
type CreateReleaseInput struct {
	Env              string
	Namespace        string
	Name             string
	Description      string
	Strategy         domain.ReleaseStrategy
	TargetRuleJSON   string
	Entries          []SnapshotEntry
	BasedOnReleaseID *int64
	Actor            Actor
}

// CreateRelease 解析条目、计算快照摘要并落库草稿发布单。
func (s *ReleaseService) CreateRelease(ctx context.Context, in CreateReleaseInput) (domain.Release, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Release{}, err
	}
	if !domain.ValidEnv(in.Env) || !domain.ValidNamespace(in.Namespace) {
		return domain.Release{}, apperr.BadRequest("invalid env or namespace")
	}
	if len(in.Entries) == 0 {
		return domain.Release{}, apperr.BadRequest("release must contain at least one config entry")
	}
	releaseItems, err := s.resolveEntries(ctx, in.Env, in.Namespace, in.Entries)
	if err != nil {
		return domain.Release{}, err
	}

	releaseNo, err := s.releases.NextReleaseNo(ctx, in.Env, in.Namespace)
	if err != nil {
		return domain.Release{}, translate(err)
	}
	releaseID, err := s.ids.Next()
	if err != nil {
		return domain.Release{}, apperr.Internal("generate release id failed", err)
	}
	now := s.now().UTC()
	release := domain.Release{
		ID:               releaseID,
		Env:              in.Env,
		Namespace:        in.Namespace,
		ReleaseNo:        releaseNo,
		Name:             strings.TrimSpace(in.Name),
		Description:      in.Description,
		Status:           domain.ReleaseStatusDraft,
		Strategy:         in.Strategy,
		TargetRuleJSON:   in.TargetRuleJSON,
		SnapshotHash:     SnapshotHash(releaseItems),
		SnapshotSize:     len(releaseItems),
		BasedOnReleaseID: in.BasedOnReleaseID,
		CreatedBy:        in.Actor.SubjectID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := release.Validate(); err != nil {
		return domain.Release{}, apperr.BadRequest(err.Error())
	}
	for i := range releaseItems {
		releaseItems[i].ReleaseID = release.ID
		if err := releaseItems[i].Validate(); err != nil {
			return domain.Release{}, apperr.BadRequest(err.Error())
		}
	}
	if err := s.releases.Create(ctx, release, releaseItems); err != nil {
		return domain.Release{}, translate(err)
	}

	attrs := map[string]string{
		"env":           release.Env,
		"namespace":     release.Namespace,
		"release_no":    fmt.Sprintf("%d", release.ReleaseNo),
		"snapshot_hash": release.SnapshotHash,
		"snapshot_size": fmt.Sprintf("%d", release.SnapshotSize),
		"strategy":      string(release.Strategy),
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigChange, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, "create_release", attrs); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}
	return release, nil
}

// Submit 将草稿提交审批（draft → pending_review）。
func (s *ReleaseService) Submit(ctx context.Context, releaseID int64, actor Actor) (domain.Release, error) {
	if err := requireActor(actor); err != nil {
		return domain.Release{}, err
	}
	release, err := s.getRelease(ctx, releaseID)
	if err != nil {
		return domain.Release{}, err
	}
	if release.Status != domain.ReleaseStatusDraft {
		return domain.Release{}, apperr.Conflict("only draft releases can be submitted")
	}
	now := s.now().UTC()
	if err := s.releases.Transition(ctx, release.ID, domain.ReleaseStatusDraft,
		domain.ReleaseStatusPendingReview, actor.SubjectID, now); err != nil {
		return domain.Release{}, translate(err)
	}
	if err := s.audit.emit(ctx, actor, audit.ActionConfigChange, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, "submit",
		map[string]string{"release_no": fmt.Sprintf("%d", release.ReleaseNo)}); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}
	release.Status = domain.ReleaseStatusPendingReview
	release.SubmittedBy = actor.SubjectID
	release.UpdatedAt = now
	return release, nil
}

// ApproveInput 是审批入参。
type ApproveInput struct {
	ReleaseID int64
	Decision  domain.ReviewDecision
	Comment   string
	Actor     Actor
}

// Approve 记录审批结论并迁移状态；提交人不得自审。
func (s *ReleaseService) Approve(ctx context.Context, in ApproveInput) (domain.Release, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Release{}, err
	}
	if !in.Decision.Valid() {
		return domain.Release{}, apperr.BadRequest(fmt.Sprintf("invalid review decision %q", in.Decision))
	}
	release, err := s.getRelease(ctx, in.ReleaseID)
	if err != nil {
		return domain.Release{}, err
	}
	if release.Status != domain.ReleaseStatusPendingReview {
		return domain.Release{}, apperr.Conflict("only pending_review releases can be approved or rejected")
	}
	if release.SubmittedBy == "" {
		return domain.Release{}, apperr.Conflict("release has no submitter recorded")
	}
	if release.SubmittedBy == in.Actor.SubjectID {
		return domain.Release{}, apperr.Forbidden("submitter cannot review their own release")
	}
	if in.Decision == domain.DecisionRejected && strings.TrimSpace(in.Comment) == "" {
		return domain.Release{}, apperr.BadRequest("rejection requires a comment")
	}

	reviewID, err := s.ids.Next()
	if err != nil {
		return domain.Release{}, apperr.Internal("generate review id failed", err)
	}
	now := s.now().UTC()
	review := domain.ReleaseReview{
		ID:        reviewID,
		ReleaseID: release.ID,
		Reviewer:  in.Actor.SubjectID,
		Decision:  in.Decision,
		Comment:   in.Comment,
		CreatedAt: now,
	}
	if err := review.Validate(); err != nil {
		return domain.Release{}, apperr.BadRequest(err.Error())
	}
	if err := s.releases.AppendReview(ctx, review); err != nil {
		return domain.Release{}, translate(err)
	}

	target := domain.ReleaseStatusApproved
	if in.Decision == domain.DecisionRejected {
		target = domain.ReleaseStatusRejected
	}
	if err := s.releases.Transition(ctx, release.ID, domain.ReleaseStatusPendingReview,
		target, in.Actor.SubjectID, now); err != nil {
		return domain.Release{}, translate(err)
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigReleaseApprove, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, "",
		map[string]string{
			"release_no": fmt.Sprintf("%d", release.ReleaseNo),
			"decision":   string(in.Decision),
		}); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}

	release.Status = target
	release.UpdatedAt = now
	if target == domain.ReleaseStatusApproved {
		release.ApprovedBy = in.Actor.SubjectID
		release.ApprovedAt = &now
	}
	return release, nil
}

// Publish 将审批通过的发布切换到生效指针（approved → active）。
func (s *ReleaseService) Publish(ctx context.Context, releaseID int64, expectedPointerVersion *int64,
	actor Actor) (domain.Release, error) {
	if err := requireActor(actor); err != nil {
		return domain.Release{}, err
	}
	release, err := s.getRelease(ctx, releaseID)
	if err != nil {
		return domain.Release{}, err
	}
	if release.Status != domain.ReleaseStatusApproved {
		return domain.Release{}, apperr.Conflict("only approved releases can be published")
	}
	pointer, previousID, err := s.switchPointer(ctx, release, expectedPointerVersion,
		actor.SubjectID, domain.ReleaseStatusSuperseded)
	if err != nil {
		return domain.Release{}, err
	}
	now := s.now().UTC()
	attrs := map[string]string{
		"release_no":      fmt.Sprintf("%d", release.ReleaseNo),
		"pointer_version": fmt.Sprintf("%d", pointer.Version),
	}
	if previousID > 0 {
		attrs["previous_release_id"] = fmt.Sprintf("%d", previousID)
	}
	if err := s.audit.emit(ctx, actor, audit.ActionConfigReleasePublish, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, "", attrs); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}
	release.Status = domain.ReleaseStatusActive
	release.PublishedAt = &now
	release.UpdatedAt = now
	return release, nil
}

// RollbackInput 是回滚入参；目标是历史 Release，不是草稿。
type RollbackInput struct {
	TargetReleaseID        int64
	Name                   string
	Description            string
	ExpectedPointerVersion *int64
	Actor                  Actor
}

// Rollback 回滚到历史发布：按目标快照生成新 Release 并立即生效。
//
// 契约（§6.3）：旧 active 转 rolled_back，新 Release 指向目标快照；
// rolled_back / superseded 本身不复活，保留完整历史链。
func (s *ReleaseService) Rollback(ctx context.Context, in RollbackInput) (domain.Release, error) {
	if err := requireActor(in.Actor); err != nil {
		return domain.Release{}, err
	}
	target, err := s.getRelease(ctx, in.TargetReleaseID)
	if err != nil {
		return domain.Release{}, err
	}
	switch target.Status {
	case domain.ReleaseStatusActive, domain.ReleaseStatusSuperseded, domain.ReleaseStatusRolledBack:
	default:
		return domain.Release{}, apperr.BadRequest("target release has never been published")
	}
	pointer, err := s.pointers.Get(ctx, target.Env, target.Namespace)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Release{}, apperr.Conflict("no active release to roll back from")
		}
		return domain.Release{}, translate(err)
	}
	if pointer.ActiveReleaseID == target.ID {
		return domain.Release{}, apperr.Conflict("target release is already active")
	}
	targetItems, err := s.releases.ListItems(ctx, target.ID)
	if err != nil {
		return domain.Release{}, translate(err)
	}
	if len(targetItems) == 0 {
		return domain.Release{}, apperr.Internal("target release snapshot is empty", nil)
	}

	releaseNo, err := s.releases.NextReleaseNo(ctx, target.Env, target.Namespace)
	if err != nil {
		return domain.Release{}, translate(err)
	}
	releaseID, err := s.ids.Next()
	if err != nil {
		return domain.Release{}, apperr.Internal("generate release id failed", err)
	}
	now := s.now().UTC()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = fmt.Sprintf("rollback to release %d", target.ReleaseNo)
	}
	description := in.Description
	if strings.TrimSpace(description) == "" {
		description = fmt.Sprintf("回滚到 release #%d（%s）", target.ReleaseNo, target.Name)
	}
	newRelease := domain.Release{
		ID:               releaseID,
		Env:              target.Env,
		Namespace:        target.Namespace,
		ReleaseNo:        releaseNo,
		Name:             name,
		Description:      description,
		Status:           domain.ReleaseStatusApproved,
		Strategy:         target.Strategy,
		TargetRuleJSON:   target.TargetRuleJSON,
		SnapshotHash:     target.SnapshotHash,
		SnapshotSize:     len(targetItems),
		BasedOnReleaseID: &pointer.ActiveReleaseID,
		CreatedBy:        in.Actor.SubjectID,
		ApprovedBy:       in.Actor.SubjectID,
		ApprovedAt:       &now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := newRelease.Validate(); err != nil {
		return domain.Release{}, apperr.BadRequest(err.Error())
	}
	newItems := make([]domain.ReleaseItem, 0, len(targetItems))
	for _, item := range targetItems {
		newItems = append(newItems, domain.ReleaseItem{
			ReleaseID:   newRelease.ID,
			ItemID:      item.ItemID,
			ItemVersion: item.ItemVersion,
			ConfigKey:   item.ConfigKey,
			ValueHash:   item.ValueHash,
		})
	}
	if SnapshotHash(newItems) != target.SnapshotHash {
		return domain.Release{}, apperr.Internal("rollback snapshot hash mismatch", nil)
	}
	if err := s.releases.Create(ctx, newRelease, newItems); err != nil {
		return domain.Release{}, translate(err)
	}

	switched, previousID, err := s.switchPointer(ctx, newRelease, in.ExpectedPointerVersion,
		in.Actor.SubjectID, domain.ReleaseStatusRolledBack)
	if err != nil {
		return domain.Release{}, err
	}
	attrs := map[string]string{
		"target_release_no": fmt.Sprintf("%d", target.ReleaseNo),
		"new_release_no":    fmt.Sprintf("%d", newRelease.ReleaseNo),
		"pointer_version":   fmt.Sprintf("%d", switched.Version),
	}
	if previousID > 0 {
		attrs["previous_release_id"] = fmt.Sprintf("%d", previousID)
	}
	if err := s.audit.emit(ctx, in.Actor, audit.ActionConfigReleaseRollback, ResourceTypeRelease,
		releaseResourceID(newRelease), audit.OutcomeSuccess, "", attrs); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}
	newRelease.Status = domain.ReleaseStatusActive
	newRelease.PublishedAt = &now
	newRelease.UpdatedAt = now
	return newRelease, nil
}

// Cancel 取消未生效的发布（draft / pending_review / approved）。
func (s *ReleaseService) Cancel(ctx context.Context, releaseID int64, reason string, actor Actor) (domain.Release, error) {
	if err := requireActor(actor); err != nil {
		return domain.Release{}, err
	}
	release, err := s.getRelease(ctx, releaseID)
	if err != nil {
		return domain.Release{}, err
	}
	switch release.Status {
	case domain.ReleaseStatusDraft, domain.ReleaseStatusPendingReview, domain.ReleaseStatusApproved:
	default:
		return domain.Release{}, apperr.Conflict("only releases that have not taken effect can be cancelled")
	}
	now := s.now().UTC()
	if err := s.releases.Transition(ctx, release.ID, release.Status,
		domain.ReleaseStatusCancelled, actor.SubjectID, now); err != nil {
		return domain.Release{}, translate(err)
	}
	if err := s.audit.emit(ctx, actor, audit.ActionConfigChange, ResourceTypeRelease,
		releaseResourceID(release), audit.OutcomeSuccess, reason,
		map[string]string{"release_no": fmt.Sprintf("%d", release.ReleaseNo)}); err != nil {
		return domain.Release{}, apperr.Internal("emit config audit failed", err)
	}
	release.Status = domain.ReleaseStatusCancelled
	release.UpdatedAt = now
	return release, nil
}

// Get 读取发布单。
func (s *ReleaseService) Get(ctx context.Context, releaseID int64) (domain.Release, error) {
	return s.getRelease(ctx, releaseID)
}

// List 按状态列出发布单（release_no 降序）。
func (s *ReleaseService) List(ctx context.Context, env, namespace string,
	status domain.ReleaseStatus, limit, offset int) ([]domain.Release, int, error) {
	if !domain.ValidEnv(env) || !domain.ValidNamespace(namespace) {
		return nil, 0, apperr.BadRequest("invalid env or namespace")
	}
	if status != "" && !status.Valid() {
		return nil, 0, apperr.BadRequest(fmt.Sprintf("invalid release status %q", status))
	}
	releases, total, err := s.releases.List(ctx, env, namespace, status, limit, offset)
	if err != nil {
		return nil, 0, translate(err)
	}
	return releases, total, nil
}

// ListItems 返回发布快照条目。
func (s *ReleaseService) ListItems(ctx context.Context, releaseID int64) ([]domain.ReleaseItem, error) {
	items, err := s.releases.ListItems(ctx, releaseID)
	if err != nil {
		return nil, translate(err)
	}
	return items, nil
}

// ListReviews 返回审批记录。
func (s *ReleaseService) ListReviews(ctx context.Context, releaseID int64) ([]domain.ReleaseReview, error) {
	reviews, err := s.releases.ListReviews(ctx, releaseID)
	if err != nil {
		return nil, translate(err)
	}
	return reviews, nil
}

// resolveEntries 逐条解析快照条目并拒绝重复键。
func (s *ReleaseService) resolveEntries(ctx context.Context, env, namespace string,
	entries []SnapshotEntry) ([]domain.ReleaseItem, error) {
	seen := make(map[int64]struct{}, len(entries))
	items := make([]domain.ReleaseItem, 0, len(entries))
	for _, entry := range entries {
		if entry.ItemID <= 0 || entry.ItemVersion <= 0 {
			return nil, apperr.BadRequest("item_id and item_version must be positive")
		}
		if _, dup := seen[entry.ItemID]; dup {
			return nil, apperr.BadRequest("duplicate item in release entries")
		}
		seen[entry.ItemID] = struct{}{}
		item, err := s.items.GetByID(ctx, entry.ItemID)
		if err != nil {
			return nil, translate(err)
		}
		if item.Env != env || item.Namespace != namespace {
			return nil, apperr.BadRequest("release entry item does not belong to the target env and namespace")
		}
		if item.Status == domain.ItemStatusArchived {
			return nil, apperr.BadRequest(fmt.Sprintf("item %q is archived", item.Key))
		}
		version, err := s.items.GetVersion(ctx, item.ID, entry.ItemVersion)
		if err != nil {
			return nil, translate(err)
		}
		items = append(items, domain.ReleaseItem{
			ItemID:      item.ID,
			ItemVersion: version.Version,
			ConfigKey:   item.Key,
			ValueHash:   version.ValueHash,
		})
	}
	return items, nil
}

// switchPointer 执行指针 CAS 并迁移新旧发布状态。
//
// prevStatus 指定被替换的 active 发布迁移到 superseded（正常发布）或
// rolled_back（回滚）。首次发布无旧指针时直接创建，版本从 1 开始。
func (s *ReleaseService) switchPointer(ctx context.Context, release domain.Release,
	expected *int64, actorID string, prevStatus domain.ReleaseStatus) (domain.Pointer, int64, error) {
	now := s.now().UTC()
	pointer, err := s.pointers.Get(ctx, release.Env, release.Namespace)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.Pointer{}, 0, translate(err)
		}
		if expected != nil && *expected != 0 {
			return domain.Pointer{}, 0, apperr.Conflict("active release pointer does not exist")
		}
		created := domain.Pointer{
			Env:             release.Env,
			Namespace:       release.Namespace,
			ActiveReleaseID: release.ID,
			Version:         1,
			UpdatedAt:       now,
		}
		if err := created.Validate(); err != nil {
			return domain.Pointer{}, 0, apperr.Internal("invalid initial release pointer", err)
		}
		if err := s.pointers.Create(ctx, created); err != nil {
			return domain.Pointer{}, 0, translate(err)
		}
		if err := s.releases.Transition(ctx, release.ID, domain.ReleaseStatusApproved,
			domain.ReleaseStatusActive, actorID, now); err != nil {
			return domain.Pointer{}, 0, translate(err)
		}
		return created, 0, nil
	}

	expectedVersion := pointer.Version
	if expected != nil {
		expectedVersion = *expected
	}
	switched, err := s.pointers.CompareAndSwap(ctx, release.Env, release.Namespace,
		expectedVersion, release.ID, now)
	if err != nil {
		return domain.Pointer{}, 0, translate(err)
	}
	previousID := pointer.ActiveReleaseID
	if previousID != release.ID {
		if err := s.releases.Transition(ctx, previousID,
			domain.ReleaseStatusActive, prevStatus, actorID, now); err != nil {
			return domain.Pointer{}, 0, translate(err)
		}
	}
	if err := s.releases.Transition(ctx, release.ID, domain.ReleaseStatusApproved,
		domain.ReleaseStatusActive, actorID, now); err != nil {
		return domain.Pointer{}, 0, translate(err)
	}
	return switched, previousID, nil
}

// getRelease 读取发布单并映射错误。
func (s *ReleaseService) getRelease(ctx context.Context, releaseID int64) (domain.Release, error) {
	if releaseID <= 0 {
		return domain.Release{}, apperr.BadRequest("release_id must be positive")
	}
	release, err := s.releases.Get(ctx, releaseID)
	if err != nil {
		return domain.Release{}, translate(err)
	}
	return release, nil
}

// releaseResourceID 返回审计资源标识：env/namespace/release_no。
func releaseResourceID(release domain.Release) string {
	return fmt.Sprintf("%s/%s/%d", release.Env, release.Namespace, release.ReleaseNo)
}

// SnapshotHash 计算发布快照摘要。
//
// 规范化：按 config_key、item_id 排序后对 "key\0version\0value_hash\n"
// 取 SHA-256；与条目顺序、数据库返回顺序无关，保证可复现。
func SnapshotHash(items []domain.ReleaseItem) string {
	sorted := append([]domain.ReleaseItem(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ConfigKey == sorted[j].ConfigKey {
			return sorted[i].ItemID < sorted[j].ItemID
		}
		return sorted[i].ConfigKey < sorted[j].ConfigKey
	})
	var builder strings.Builder
	for _, item := range sorted {
		builder.WriteString(item.ConfigKey)
		builder.WriteByte(0)
		fmt.Fprintf(&builder, "%d", item.ItemVersion)
		builder.WriteByte(0)
		builder.WriteString(item.ValueHash)
		builder.WriteByte('\n')
	}
	return domain.HashValue(builder.String())
}

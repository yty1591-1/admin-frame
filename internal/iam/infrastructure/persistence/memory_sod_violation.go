package persistence

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemorySodViolationRepository 是内存版互斥组存量预检实现。
//
// 语义与 SQL 实现一致：按主体展开有效角色闭包后与候选组求交，
// 仅返回超过阈值的记录；用于单测与 local / demo。
type MemorySodViolationRepository struct {
	roles    domain.RoleRepository
	bindings domain.BindingRepository
}

// NewMemorySodViolationRepository 组合角色 / 绑定仓储构造预检实现。
func NewMemorySodViolationRepository(
	roles domain.RoleRepository, bindings domain.BindingRepository,
) *MemorySodViolationRepository {
	return &MemorySodViolationRepository{roles: roles, bindings: bindings}
}

// List 返回候选互斥组在 at 时刻的存量违规主体。
func (r *MemorySodViolationRepository) List(
	ctx context.Context, group *domain.SodGroup, at time.Time, limit int,
) ([]domain.SodViolation, error) {
	if group == nil {
		return nil, apperr.BadRequest("sod group is required")
	}
	if r == nil || r.roles == nil || r.bindings == nil {
		return nil, apperr.Unavailable("iam sod violation repository is not configured")
	}
	if len(group.RoleIDs) == 0 {
		return []domain.SodViolation{}, nil
	}
	limit = normalizeSodViolationLimit(limit)

	members := make(map[string]struct{}, len(group.RoleIDs))
	for _, roleID := range group.RoleIDs {
		members[roleID] = struct{}{}
	}

	bindings, err := r.bindings.ListEffectiveByTenant(ctx, group.Channel, group.MerchantID, at)
	if err != nil {
		return nil, err
	}

	type subjectKey struct {
		subjectType domain.SubjectType
		subjectID   string
	}
	grouped := make(map[subjectKey][]string)
	for _, binding := range bindings {
		key := subjectKey{subjectType: binding.SubjectType, subjectID: binding.SubjectID}
		grouped[key] = append(grouped[key], binding.RoleID)
	}
	keys := make([]subjectKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b subjectKey) int {
		if byID := strings.Compare(a.subjectID, b.subjectID); byID != 0 {
			return byID
		}
		return strings.Compare(string(a.subjectType), string(b.subjectType))
	})

	out := make([]domain.SodViolation, 0, limit)
	for _, key := range keys {
		closure := make(map[string]struct{})
		for _, roleID := range grouped[key] {
			role, err := r.roles.FindByID(ctx, roleID)
			if err != nil {
				if errors.Is(err, apperr.ErrNotFound) {
					continue
				}
				return nil, err
			}
			if !role.IsActive() || role.Type == domain.RoleTypeBreakglass {
				continue
			}
			closure[roleID] = struct{}{}
			ancestors, err := r.roles.ListAncestors(ctx, roleID)
			if err != nil {
				if errors.Is(err, apperr.ErrNotFound) {
					continue
				}
				return nil, err
			}
			for _, ancestor := range ancestors {
				if !ancestor.IsActive() || ancestor.Type == domain.RoleTypeBreakglass {
					continue
				}
				closure[ancestor.ID] = struct{}{}
			}
		}

		hits := make([]string, 0, len(members))
		for roleID := range closure {
			if _, ok := members[roleID]; ok {
				hits = append(hits, roleID)
			}
		}
		if len(hits) <= group.MaxActiveRoles {
			continue
		}
		slices.Sort(hits)
		out = append(out, domain.SodViolation{
			Channel:     group.Channel,
			SubjectType: key.subjectType,
			SubjectID:   key.subjectID,
			MerchantID:  group.MerchantID,
			RoleIDs:     hits,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

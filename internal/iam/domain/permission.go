package domain

import (
	"regexp"
	"slices"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
)

const (
	// MaxPermissionCodeLen 权限码最大字节数，与 iam_permission.code 列宽一致。
	MaxPermissionCodeLen = 128
	// MinPermissionSegments 权限码最少段数（resource:action）。
	MinPermissionSegments = 2
	// MaxPermissionSegments 权限码最多段数（domain:resource:action）。
	MaxPermissionSegments = 4
)

// permissionCodePattern 与 catalog 保持同一语法：全小写，段间以 ":" 分隔。
//
// 通配符在校验中单独拒绝，以便给出可行动的错误信息（区分「格式错」与「不允许通配」）。
var permissionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*(?::[a-z][a-z0-9_]*){0,2}$`)

// ValidatePermissionCode 校验权限码格式并拒绝通配符。
//
// 通配符（"*" 或 "resource:*"）只允许 breakglass 角色在审批有效期内由 app 层动态展开，
// 任何写入路径（角色授权、直接授予）都不得持久化通配符，否则一次误授权即成为永久超管。
func ValidatePermissionCode(code iam.PermissionCode) error {
	s := string(code)
	if s == "" {
		return errInvalid(ErrInvalidPermissionCode, "permission code is required")
	}
	if len(s) > MaxPermissionCodeLen {
		return errInvalid(ErrInvalidPermissionCode, "permission code %q exceeds %d bytes", s, MaxPermissionCodeLen)
	}
	if strings.ContainsRune(s, '*') {
		return errInvalid(ErrWildcardNotAllowed, "permission code %q must not contain wildcard *", s)
	}
	if !permissionCodePattern.MatchString(s) {
		return errInvalid(ErrInvalidPermissionCode,
			"permission code %q must be lowercase %d-%d segments separated by ':' (e.g. order:read)",
			s, MinPermissionSegments, MaxPermissionSegments)
	}
	return nil
}

// PermissionSet 是权限码集合，保证「无重复、无通配符、可稳定排序」。
//
// 使用 map 而不是切片：授权替换、包含判断与差集都是集合语义；
// 需要落库或写审计时通过 Sorted 得到确定性顺序。
type PermissionSet map[iam.PermissionCode]struct{}

// NewPermissionSet 由权限码切片构造集合：校验每个码并去重。
//
// 返回的集合永不为 nil，调用方可直接读写而无需判空。
func NewPermissionSet(codes []iam.PermissionCode) (PermissionSet, error) {
	set := make(PermissionSet, len(codes))
	for _, code := range codes {
		if err := ValidatePermissionCode(code); err != nil {
			return nil, err
		}
		set[code] = struct{}{}
	}
	return set, nil
}

// Clone 返回副本；空集合返回空集合（非 nil）。
func (s PermissionSet) Clone() PermissionSet {
	out := make(PermissionSet, len(s))
	for code := range s {
		out[code] = struct{}{}
	}
	return out
}

// Contains 判断是否包含指定权限码。
func (s PermissionSet) Contains(code iam.PermissionCode) bool {
	_, ok := s[code]
	return ok
}

// Len 返回权限点个数。
func (s PermissionSet) Len() int { return len(s) }

// Sorted 返回按字典序排列的权限码切片，用于落库、审计与响应，保证确定性。
func (s PermissionSet) Sorted() []iam.PermissionCode {
	out := make([]iam.PermissionCode, 0, len(s))
	for code := range s {
		out = append(out, code)
	}
	slices.Sort(out)
	return out
}

// String 返回逗号分隔的权限码，供日志与错误信息使用。
func (s PermissionSet) String() string {
	codes := s.Sorted()
	parts := make([]string, len(codes))
	for i, code := range codes {
		parts[i] = string(code)
	}
	return strings.Join(parts, ",")
}

// PermissionChange 描述一次授权替换的差量，用于审计事件（只记录变更，不记录全量）。
type PermissionChange struct {
	// Added 新增的权限码，按字典序。
	Added []iam.PermissionCode
	// Removed 撤销的权限码，按字典序。
	Removed []iam.PermissionCode
}

// IsEmpty 表示授权集合未发生变化。
func (c PermissionChange) IsEmpty() bool {
	return len(c.Added) == 0 && len(c.Removed) == 0
}

// Diff 计算从当前集合变为 next 的差量。
func (s PermissionSet) Diff(next PermissionSet) PermissionChange {
	var change PermissionChange
	for code := range next {
		if !s.Contains(code) {
			change.Added = append(change.Added, code)
		}
	}
	for code := range s {
		if !next.Contains(code) {
			change.Removed = append(change.Removed, code)
		}
	}
	slices.Sort(change.Added)
	slices.Sort(change.Removed)
	return change
}

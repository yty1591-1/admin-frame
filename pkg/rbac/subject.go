package rbac

import "strings"

// Subject 是待鉴权主体的权限快照（角色名 + 直接权限点）。
type Subject struct {
	Roles       []string
	Permissions []Permission
}

// Clone 返回深拷贝。
func (s Subject) Clone() Subject {
	return Subject{
		Roles:       append([]string(nil), s.Roles...),
		Permissions: append([]Permission(nil), s.Permissions...),
	}
}

func normalizeRoles(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		n := strings.ToLower(strings.TrimSpace(r))
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

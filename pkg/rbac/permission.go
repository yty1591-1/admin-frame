package rbac

import "strings"

// Permission 是稳定的权限点标识，建议 resource:action。
type Permission string

// Normalize 去空白并转小写，空串保持为空。
func (p Permission) Normalize() Permission {
	return Permission(strings.ToLower(strings.TrimSpace(string(p))))
}

// String 返回字符串形式。
func (p Permission) String() string { return string(p.Normalize()) }

// Valid 报告规范化后是否非空。
func (p Permission) Valid() bool { return p.Normalize() != "" }

// Matches 报告本授予项是否覆盖 need（本值为 grant）。
//
//	*           → 任意
//	order:*     → order:read / order:write
//	order:read  → 精确匹配
func (p Permission) Matches(need Permission) bool {
	grant := p.Normalize()
	req := need.Normalize()
	if grant == "" || req == "" {
		return false
	}
	if grant == "*" || grant == req {
		return true
	}
	if strings.HasSuffix(string(grant), ":*") {
		prefix := strings.TrimSuffix(string(grant), "*")
		return strings.HasPrefix(string(req), prefix)
	}
	return false
}

func normalizeList(perms []Permission) []Permission {
	if len(perms) == 0 {
		return nil
	}
	out := make([]Permission, 0, len(perms))
	seen := make(map[Permission]struct{}, len(perms))
	for _, p := range perms {
		n := p.Normalize()
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

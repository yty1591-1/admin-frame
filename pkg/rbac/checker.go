package rbac

import "sync"

// Checker 角色→权限注册表与判定器（并发安全）。
type Checker struct {
	mu         sync.RWMutex
	roles      map[string][]Permission
	superRoles map[string]struct{}
	wildcards  bool
}

// Option 配置 Checker。
type Option func(*Checker)

// WithSuperRoles 指定拥有全部权限的角色（如 admin）；默认无超级角色。
func WithSuperRoles(roles ...string) Option {
	return func(c *Checker) {
		for _, r := range normalizeRoles(roles) {
			c.superRoles[r] = struct{}{}
		}
	}
}

// WithWildcards 是否启用 * / resource:* 匹配；默认 true。
func WithWildcards(enabled bool) Option {
	return func(c *Checker) { c.wildcards = enabled }
}

// New 构造空 Checker（默认拒绝 + 通配符开启）。
func New(opts ...Option) *Checker {
	c := &Checker{
		roles:      make(map[string][]Permission),
		superRoles: make(map[string]struct{}),
		wildcards:  true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// GrantRole 为角色授予权限点（可多次调用合并；覆盖同角色时追加去重）。
func (c *Checker) GrantRole(role string, perms ...Permission) {
	if c == nil {
		return
	}
	r := ""
	if rs := normalizeRoles([]string{role}); len(rs) == 1 {
		r = rs[0]
	}
	if r == "" {
		return
	}
	list := normalizeList(perms)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roles[r] = mergePerms(c.roles[r], list)
}

// RevokeRole 移除角色全部授予。
func (c *Checker) RevokeRole(role string) {
	if c == nil {
		return
	}
	rs := normalizeRoles([]string{role})
	if len(rs) != 1 {
		return
	}
	c.mu.Lock()
	delete(c.roles, rs[0])
	c.mu.Unlock()
}

// RolePermissions 返回角色已授予权限的副本。
func (c *Checker) RolePermissions(role string) []Permission {
	if c == nil {
		return nil
	}
	rs := normalizeRoles([]string{role})
	if len(rs) != 1 {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Permission(nil), c.roles[rs[0]]...)
}

// Allow 判定主体是否拥有 need（默认拒绝）。
func (c *Checker) Allow(sub Subject, need Permission) bool {
	if c == nil || !need.Valid() {
		return false
	}
	need = need.Normalize()
	roles := normalizeRoles(sub.Roles)
	direct := normalizeList(sub.Permissions)

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, r := range roles {
		if _, ok := c.superRoles[r]; ok {
			return true
		}
	}
	if matchesAny(direct, need, c.wildcards) {
		return true
	}
	for _, r := range roles {
		if matchesAny(c.roles[r], need, c.wildcards) {
			return true
		}
	}
	return false
}

// AllowAny 拥有任一权限即通过。
func (c *Checker) AllowAny(sub Subject, needs ...Permission) bool {
	for _, n := range needs {
		if c.Allow(sub, n) {
			return true
		}
	}
	return false
}

// AllowAll 须同时拥有全部权限。
func (c *Checker) AllowAll(sub Subject, needs ...Permission) bool {
	if len(needs) == 0 {
		return false
	}
	for _, n := range needs {
		if !c.Allow(sub, n) {
			return false
		}
	}
	return true
}

// Expand 返回主体有效权限点并集（不含超级角色的隐式 *）。
func (c *Checker) Expand(sub Subject) []Permission {
	if c == nil {
		return nil
	}
	roles := normalizeRoles(sub.Roles)
	out := normalizeList(sub.Permissions)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, r := range roles {
		if _, ok := c.superRoles[r]; ok {
			return []Permission{"*"}
		}
		out = mergePerms(out, c.roles[r])
	}
	return out
}

func mergePerms(a, b []Permission) []Permission {
	return normalizeList(append(append([]Permission(nil), a...), b...))
}

func matchesAny(grants []Permission, need Permission, wildcards bool) bool {
	for _, g := range grants {
		if wildcards {
			if g.Matches(need) {
				return true
			}
			continue
		}
		if g.Normalize() == need.Normalize() {
			return true
		}
	}
	return false
}

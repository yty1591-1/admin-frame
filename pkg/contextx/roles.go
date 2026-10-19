package contextx

import "context"

// WithRoles 写入角色副本（调用方切片随后可改，不影响 context 内数据）。
func WithRoles(ctx context.Context, roles ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(roles) == 0 {
		return ctx
	}
	cp := append([]string(nil), roles...)
	return context.WithValue(ctx, rolesKey{}, cp)
}

// Roles 返回角色副本；缺失返回 nil。
func Roles(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(rolesKey{}).([]string)
	if !ok || len(v) == 0 {
		return nil
	}
	return append([]string(nil), v...)
}

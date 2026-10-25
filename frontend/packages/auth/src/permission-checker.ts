export type PermissionChecker = {
  readonly size: number;
  has(code: string): boolean;
  hasAny(codes: readonly string[]): boolean;
};

const DENY_ALL: PermissionChecker = {
  size: 0,
  has: () => false,
  hasAny: () => false,
};

/**
 * 权限判定集中入口（FE-10）：默认拒绝，快照缺失等价于无权限。
 * 前端判定仅用于可见性，服务端始终是最终裁决（§10.4）。
 */
export function createPermissionChecker(permissions?: Iterable<string> | null): PermissionChecker {
  if (!permissions) {
    return DENY_ALL;
  }
  const set = new Set(permissions);
  return {
    size: set.size,
    has: (code) => set.has(code),
    hasAny: (codes) => codes.some((code) => set.has(code)),
  };
}

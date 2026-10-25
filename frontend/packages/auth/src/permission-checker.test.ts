import { describe, expect, it } from 'vitest';
import { createPermissionChecker } from './permission-checker';

describe('createPermissionChecker', () => {
  it('快照缺失时默认拒绝', () => {
    const checker = createPermissionChecker();
    expect(checker.has('iam:role:read')).toBe(false);
    expect(checker.hasAny(['iam:role:read'])).toBe(false);
    expect(checker.size).toBe(0);
  });

  it('按权限点判定，任一满足即可', () => {
    const checker = createPermissionChecker(['iam:role:read', 'audit:log:read']);
    expect(checker.has('iam:role:read')).toBe(true);
    expect(checker.has('iam:role:write')).toBe(false);
    expect(checker.hasAny(['iam:role:write', 'audit:log:read'])).toBe(true);
    expect(checker.size).toBe(2);
  });
});

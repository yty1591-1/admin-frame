import { describe, expect, it } from 'vitest';

import { hasUnusedDependencies } from './has-unused';

import type { KnipFileIssue, KnipResult } from './has-unused';

function makeIssue(overrides: Partial<KnipFileIssue>): KnipFileIssue {
  return {
    dependencies: [],
    devDependencies: [],
    file: 'apps/web-admin/package.json',
    optionalPeerDependencies: [],
    ...overrides,
  };
}

describe('hasUnusedDependencies', () => {
  it('没有问题时不判失败', () => {
    const result: KnipResult = { issues: [] };
    expect(hasUnusedDependencies(result)).toBe(false);
  });

  it('存在未使用 dependencies 时判失败', () => {
    const result: KnipResult = {
      issues: [
        makeIssue({
          dependencies: [{ col: 1, line: 1, name: 'dayjs', pos: 1 }],
        }),
      ],
    };
    expect(hasUnusedDependencies(result)).toBe(true);
  });

  it('存在未使用 devDependencies 时判失败', () => {
    const result: KnipResult = {
      issues: [
        makeIssue({
          devDependencies: [{ col: 1, line: 1, name: 'vitest', pos: 1 }],
        }),
      ],
    };
    expect(hasUnusedDependencies(result)).toBe(true);
  });

  it('仅 optionalPeerDependencies 不计入失败', () => {
    const result: KnipResult = {
      issues: [
        makeIssue({
          optionalPeerDependencies: [{ col: 1, line: 1, name: 'vue', pos: 1 }],
        }),
      ],
    };
    expect(hasUnusedDependencies(result)).toBe(false);
  });
});

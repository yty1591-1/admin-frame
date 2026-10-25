import type { ViteUserConfig } from 'vitest/config';

/**
 * 共享 Vitest 基线（FE-18）。

 * 覆盖率阈值按 TESTING_STANDARDS 与前端架构 §14.1：
 * 行/函数/分支/语句 ≥ 70%，生成物与装配 barrel 不计入。
 */
export const sharedTestConfig: ViteUserConfig['test'] = {
  environment: 'jsdom',
  setupFiles: ['./vitest.setup.ts'],
  include: ['packages/*/src/**/*.test.{ts,tsx}'],
  passWithNoTests: false,
  coverage: {
    provider: 'v8',
    reporter: ['text', 'lcov'],
    include: ['packages/*/src/**/*.{ts,tsx}'],
    exclude: [
      '**/*.test.{ts,tsx}',
      '**/index.ts',
      'packages/api-client/src/generated/**',
      'packages/testing/**',
    ],
    thresholds: {
      lines: 70,
      functions: 70,
      branches: 70,
      statements: 70,
    },
  },
};

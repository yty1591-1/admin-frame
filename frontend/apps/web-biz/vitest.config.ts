import { fileURLToPath, URL } from 'node:url';

import Vue from '@vitejs/plugin-vue';
import VueJsx from '@vitejs/plugin-vue-jsx';
import { defineConfig } from 'vitest/config';

/**
 * B 端应用级冒烟用例。
 *
 * 与仓库根 `vitest.config.ts` 分开运行：`#/` 别名必须指向本应用 `src`，
 * 而两个应用的 `#/` 语义不同（对应 FE-G-06）。
 * 覆盖率只统计“核心运行层”，视图随 FE-T-01 逐步扩大。
 */
export default defineConfig({
  plugins: [Vue(), VueJsx()],
  resolve: {
    alias: [
      {
        find: /^#\//,
        replacement: fileURLToPath(new URL('src/', import.meta.url)),
      },
    ],
  },
  test: {
    root: fileURLToPath(new URL('.', import.meta.url)),
    environment: 'happy-dom',
    environmentOptions: {
      happyDOM: {
        settings: {
          handleDisabledFileLoadingAsSuccess: true,
        },
      },
    },
    exclude: ['**/node_modules/**', '**/dist/**', '**/e2e/**'],
    coverage: {
      enabled: true,
      provider: 'v8',
      reporter: ['text-summary', 'json-summary'],
      reportsDirectory: './.coverage',
      include: [
        'src/router/access.ts',
        'src/router/guard.ts',
        'src/store/auth.ts',
      ],
      exclude: ['**/*.test.ts', '**/__tests__/**'],
      thresholds: {
        branches: 30,
        functions: 30,
        lines: 30,
        statements: 30,
      },
    },
  },
});

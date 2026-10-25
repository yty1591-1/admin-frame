import { createEslintBase } from './packages/config/src/eslint-base.mjs';

export default [
  {
    ignores: [
      '**/node_modules/**',
      '**/dist/**',
      '**/coverage/**',
      'pnpm-lock.yaml',
      'packages/api-client/src/generated/**',
      'playwright-report/**',
      'test-results/**',
    ],
  },
  ...createEslintBase({ tsconfigRootDir: import.meta.dirname }),
];

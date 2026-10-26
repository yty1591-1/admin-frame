import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import importPlugin from 'eslint-plugin-import';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';
import tseslint from 'typescript-eslint';

const FETCH_RESTRICTION = {
  name: 'fetch',
  message: '仅 packages/api-client 允许发起 HTTP 请求（FE-06）',
};

const TOKEN_RESTRICTIONS = [
  {
    name: 'localStorage',
    message: '禁止使用 localStorage 存储令牌或其他会话数据（FE-07）',
  },
  {
    name: 'sessionStorage',
    message: '会话存储只能由 packages/auth 通过 TokenStore 访问（FE-07）',
  },
];

const ANT_DESIGN_RESTRICTION = {
  name: 'antd',
  message: '业务代码统一从 @admin-frame/ui 导入，禁止直接依赖 antd（§11.1）',
};

const AXIOS_RESTRICTION = {
  name: 'axios',
  message: 'HTTP 请求只能通过 @admin-frame/api-client 发出（FE-06）',
};

const REACT_ROUTER_DOM_RESTRICTION = {
  name: 'react-router-dom',
  message: '统一使用 react-router（React Router 7）导入路由 API',
};

const PRIVATE_PACKAGE_PATTERNS = [
  {
    group: ['@admin-frame/*/src/*', '@admin-frame/*/dist/*'],
    message: '包内实现属于私有细节，只能从包公共出口导入（FE-04）',
  },
  {
    group: ['**/internal/*'],
    message: '禁止深链 feature 内部实现，请从 index.ts 公共出口导入（FE-04）',
  },
];

const OTEL_RESTRICTION = {
  group: ['@opentelemetry/*'],
  message: 'OTel 依赖仅允许 packages/observability 引入（D3-02）',
};

/**
 * 前端 ESLint 基线（FE-01～FE-07、FE-13、FE-14）。
 * 供根配置与各 app 复用；apps 不得自行关闭这些规则。
 * @param {{ tsconfigRootDir: string }} options
 */
export function createEslintBase({ tsconfigRootDir }) {
  return tseslint.config(
    js.configs.recommended,
    ...tseslint.configs.recommendedTypeChecked,
    {
      languageOptions: {
        parserOptions: {
          projectService: true,
          tsconfigRootDir,
        },
        globals: {
          ...globals.browser,
          ...globals.node,
        },
      },
      plugins: {
        'react-hooks': reactHooks,
        'jsx-a11y': jsxA11y,
        import: importPlugin,
      },
      settings: {
        'import/resolver': {
          typescript: { project: 'tsconfig.base.json' },
        },
      },
      rules: {
        ...jsxA11y.flatConfigs.recommended.rules,
        'react-hooks/rules-of-hooks': 'error',
        'react-hooks/exhaustive-deps': 'error',
        'no-console': 'error',
        'no-debugger': 'error',
        'no-eval': 'error',
        'no-implied-eval': 'error',
        'no-new-func': 'error',
        'no-restricted-syntax': [
          'error',
          {
            selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
            message: '禁止 dangerouslySetInnerHTML；确需渲染富文本须先净化并注释理由（FE-14）',
          },
        ],
        eqeqeq: ['error', 'smart'],
        'prefer-const': 'error',
        'no-restricted-globals': ['error', FETCH_RESTRICTION, ...TOKEN_RESTRICTIONS],
        'no-restricted-properties': [
          'error',
          {
            object: 'window',
            property: 'localStorage',
            message: '禁止使用 localStorage 存储令牌（FE-07）',
          },
          {
            object: 'window',
            property: 'sessionStorage',
            message: '会话存储只能由 packages/auth 访问（FE-07）',
          },
        ],
        'no-restricted-imports': [
          'error',
          {
            paths: [ANT_DESIGN_RESTRICTION, AXIOS_RESTRICTION, REACT_ROUTER_DOM_RESTRICTION],
            patterns: [...PRIVATE_PACKAGE_PATTERNS, OTEL_RESTRICTION],
          },
        ],
        '@typescript-eslint/consistent-type-imports': [
          'error',
          { prefer: 'type-imports', fixStyle: 'separate-type-imports' },
        ],
        '@typescript-eslint/no-explicit-any': 'error',
        '@typescript-eslint/no-non-null-assertion': 'error',
        '@typescript-eslint/no-unused-vars': [
          'error',
          { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
        ],
        '@typescript-eslint/restrict-template-expressions': [
          'error',
          { allowNumber: true, allowBoolean: true },
        ],
        'import/no-duplicates': 'error',
      },
    },
    {
      files: ['packages/api-client/**/*.{ts,tsx}'],
      rules: {
        // 唯一允许发起 HTTP 的包；仍禁止任何令牌存储访问。
        'no-restricted-globals': ['error', ...TOKEN_RESTRICTIONS],
        'no-restricted-properties': 'off',
      },
    },
    {
      files: ['packages/auth/**/*.{ts,tsx}'],
      rules: {
        // 唯一允许访问会话存储的包；仍禁止 localStorage 与 fetch。
        'no-restricted-globals': ['error', FETCH_RESTRICTION, TOKEN_RESTRICTIONS[0]],
        'no-restricted-properties': [
          'error',
          {
            object: 'window',
            property: 'localStorage',
            message: '禁止使用 localStorage 存储令牌（FE-07）',
          },
          {
            object: 'localStorage',
            property: 'setItem',
            message: '禁止使用 localStorage 存储令牌（FE-07）',
          },
        ],
      },
    },
    {
      files: ['packages/ui/**/*.{ts,tsx}'],
      rules: {
        'no-restricted-imports': [
          'error',
          {
            paths: [AXIOS_RESTRICTION, REACT_ROUTER_DOM_RESTRICTION],
            patterns: PRIVATE_PACKAGE_PATTERNS,
          },
        ],
      },
    },
    {
      files: ['packages/observability/**/*.{ts,tsx}'],
      rules: {
        // D3-02：observability 是唯一允许引入 OTel 的包；antd/axios 仍禁止。
        'no-restricted-imports': [
          'error',
          {
            paths: [ANT_DESIGN_RESTRICTION, AXIOS_RESTRICTION, REACT_ROUTER_DOM_RESTRICTION],
            patterns: PRIVATE_PACKAGE_PATTERNS,
          },
        ],
      },
    },
    {
      files: ['packages/observability/src/chunk-reload.ts'],
      rules: {
        // 唯一 sessionStorage 例外：仅存一次性刷新标记，键名与值不含令牌（FE-07、D3-04）。
        'no-restricted-globals': ['error', FETCH_RESTRICTION, TOKEN_RESTRICTIONS[0]],
        'no-restricted-properties': [
          'error',
          {
            object: 'window',
            property: 'localStorage',
            message: '禁止使用 localStorage 存储令牌（FE-07）',
          },
        ],
      },
    },
    {
      files: ['apps/**/src/**/*.{ts,tsx}'],
      rules: {
        // 页面路由不得占用 API 前缀（§17.4）；同时保留富文本禁令（FE-14）。
        'no-restricted-syntax': [
          'error',
          {
            selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
            message: '禁止 dangerouslySetInnerHTML；确需渲染富文本须先净化并注释理由（FE-14）',
          },
          {
            selector: 'Literal[value=/^\\/(admin|biz)(\\/|$)/]',
            message: '前端路由不得占用 /admin、/biz 前缀（§17.4）',
          },
        ],
      },
    },
    {
      files: ['**/*.config.{ts,mjs,js}', 'e2e/**/*.ts', 'scripts/**/*.mjs', 'vitest.setup.ts'],
      rules: {
        'no-console': 'off',
      },
    },
    {
      // ESLint 配置与 CI 脚本为 JS，无 TS project，关闭依赖类型信息的规则（FE-02）。
      files: ['**/*.{js,mjs,cjs}'],
      ...tseslint.configs.disableTypeChecked,
    },
    prettier,
  );
}

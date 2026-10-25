/**
 * 依赖边界门禁（FE-04 / 前端架构 §5）。
 * 只扫描 apps 与 packages 的源码，node_modules 视为终端依赖。
 * @type {import('dependency-cruiser').IConfiguration}
 */
module.exports = {
  forbidden: [
    {
      name: 'no-circular',
      comment: '禁止循环依赖（含包内与跨包）',
      severity: 'error',
      from: {},
      to: { circular: true },
    },
    {
      name: 'apps-no-cross-import',
      comment: 'A/B 两个 app 之间零引用',
      severity: 'error',
      from: { path: '^apps/[^/]+/' },
      to: { path: '^apps/', pathNot: '^apps/[^/]+/' },
    },
    {
      name: 'packages-no-apps',
      comment: 'packages 不得反向依赖 apps',
      severity: 'error',
      from: { path: '^packages/' },
      to: { path: '^apps/' },
    },
    {
      name: 'generated-only-in-api-client',
      comment: '生成物只允许被 api-client 内部消费（FE-08）',
      severity: 'error',
      from: { pathNot: '^packages/api-client/' },
      to: { path: '^packages/api-client/src/generated/' },
    },
    {
      name: 'shared-is-leaf',
      comment: 'shared 是最底层共享包，不得依赖其他 packages',
      severity: 'error',
      from: { path: '^packages/shared/' },
      to: { path: '^packages/(api-client|auth|ui|testing|config)/' },
    },
    {
      name: 'ui-not-on-api',
      comment: '设计系统不感知接口与鉴权',
      severity: 'error',
      from: { path: '^packages/ui/' },
      to: { path: '^packages/(api-client|auth|testing)/' },
    },
    {
      name: 'auth-not-on-ui',
      comment: '鉴权包不得依赖设计系统',
      severity: 'error',
      from: { path: '^packages/auth/' },
      to: { path: '^packages/(ui|testing)/' },
    },
    {
      name: 'api-client-not-on-auth',
      comment: '请求管线不得依赖鉴权包（令牌由上层注入）',
      severity: 'error',
      from: { path: '^packages/api-client/' },
      to: { path: '^packages/(auth|ui|testing)/' },
    },
    {
      name: 'apps-layer-order',
      comment: 'features/entities/shared 不得向上依赖 pages/app',
      severity: 'error',
      from: { path: '^apps/[^/]+/src/(features|entities|shared)/' },
      to: { path: '^apps/[^/]+/src/(app|pages)/' },
    },
    {
      name: 'entities-no-features',
      comment: 'entities 不得依赖 features',
      severity: 'error',
      from: { path: '^apps/[^/]+/src/entities/' },
      to: { path: '^apps/[^/]+/src/features/' },
    },
    {
      name: 'no-cross-feature',
      comment: 'feature 之间不得横向引用，组合由 pages 完成',
      severity: 'error',
      from: { path: '^apps/[^/]+/src/features/([^/]+)/' },
      to: {
        path: '^apps/[^/]+/src/features/',
        pathNot: '^apps/[^/]+/src/features/$1/',
      },
    },
    {
      name: 'only-api-client-uses-http-libs',
      comment: 'HTTP 客户端实现只能在 api-client 内出现（FE-06）',
      severity: 'error',
      from: { pathNot: '^packages/api-client/' },
      to: { path: 'node_modules/(axios|ky|undici|got)/' },
    },
  ],
  options: {
    doNotFollow: { path: 'node_modules' },
    tsConfig: { fileName: 'tsconfig.base.json' },
    exclude: {
      // 构建产物与测试报告不是源码依赖，必须排除（否则 build 后的 dist chunk 会被判循环）。
      path: 'node_modules|/dist/|/coverage/|/playwright-report/|/test-results/|\\.test\\.(ts|tsx)$',
    },
    enhancedResolveOptions: {
      exportsFields: ['exports'],
      conditionNames: ['import', 'require', 'node', 'default'],
    },
    reporterOptions: {
      dot: { collapsePattern: 'node_modules/[^/]+' },
    },
  },
};

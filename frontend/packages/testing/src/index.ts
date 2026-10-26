export { findSeriousA11yViolations } from './axe';
export type { A11yViolation } from './axe';
export { createAdminHandlers, createBizHandlers } from './handlers';
export type { HandlerOptions } from './handlers';
export {
  authSessionListFixture,
  bizSubjectPermissionsFixture,
  mfaSetupFixture,
  permissionListFixture,
  roleListFixture,
  subjectPermissionsFixture,
  tokenPairFixture,
} from './fixtures';
export { createTestQueryClient } from './query-client';
export { createTestRouter, renderWithProviders } from './render';
export type { RenderWithProvidersOptions, RenderWithProvidersResult } from './render';
export { createTestServer } from './server';
export type { TestServer } from './server';

/** 测试用运行期配置里的 API 基址；MSW handler 必须使用同一值。 */
export const TEST_API_BASE_URL = 'http://api.test';

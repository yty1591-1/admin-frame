import { setupServer } from 'msw/node';
import type { HttpHandler } from 'msw';

export type TestServer = ReturnType<typeof setupServer>;

/**
 * MSW Node 服务（§14.1）：测试内显式调用 listen/resetHandlers/close，
 * 使每个用例的契约夹具相互隔离，避免隐式全局状态。
 */
export function createTestServer(handlers: HttpHandler[]): TestServer {
  return setupServer(...handlers);
}

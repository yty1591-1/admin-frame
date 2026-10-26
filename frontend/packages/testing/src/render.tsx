import type { ReactElement } from 'react';
import { render, type RenderOptions, type RenderResult } from '@testing-library/react';
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import { ThemeProvider, ToastProvider } from '@admin-frame/ui';
import { MemoryRouter, createMemoryRouter, type RouteObject } from 'react-router';
import { createTestQueryClient } from './query-client';

export type RenderWithProvidersResult = RenderResult & {
  queryClient: QueryClient;
};

export type RenderWithProvidersOptions = Omit<RenderOptions, 'wrapper'> & {
  queryClient?: QueryClient;
  /**
   * 传入后以 `MemoryRouter` 包裹：页面依赖 `useSearchParams`/`Navigate`/`Link` 时必须提供。
   * 需要 `useBlocker` 等数据路由能力时改用 `createTestRouter` + `RouterProvider`。
   */
  initialEntries?: string[];
  /** 默认包装 `ToastProvider`（`useToast` 依赖）；纯展示组件可关闭。 */
  toast?: boolean;
};

/** 组件测试统一装配设计系统、通知、QueryClient 与可选路由。 */
export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const {
    queryClient = createTestQueryClient(),
    initialEntries,
    toast = true,
    ...renderOptions
  } = options;

  const withToast = toast ? <ToastProvider>{ui}</ToastProvider> : ui;
  const withRouter =
    initialEntries === undefined ? (
      withToast
    ) : (
      <MemoryRouter initialEntries={initialEntries}>{withToast}</MemoryRouter>
    );

  const result = render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>{withRouter}</QueryClientProvider>
    </ThemeProvider>,
    renderOptions,
  );
  return { ...result, queryClient };
}

/**
 * 数据路由（§14.1）：`useBlocker`/loader/action 只在数据路由下可用，
 * 与 `renderWithProviders` 组合使用（自身不装配 Provider）。
 */
export function createTestRouter(routes: RouteObject[], initialEntries: string[] = ['/']) {
  return createMemoryRouter(routes, { initialEntries });
}

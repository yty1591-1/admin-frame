import type { ReactElement } from 'react';
import { render, type RenderOptions, type RenderResult } from '@testing-library/react';
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import { ThemeProvider } from '@admin-frame/ui';
import { createTestQueryClient } from './query-client';

export type RenderWithProvidersResult = RenderResult & {
  queryClient: QueryClient;
};

/** 组件测试统一装配 QueryClient + 设计系统 Provider。 */
export function renderWithProviders(
  ui: ReactElement,
  options?: Omit<RenderOptions, 'wrapper'>,
): RenderWithProvidersResult {
  const queryClient = createTestQueryClient();
  const result = render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </ThemeProvider>,
    options,
  );
  return { ...result, queryClient };
}

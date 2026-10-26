import { isAppError } from '@admin-frame/api-client';
import { I18nProvider } from '@admin-frame/i18n';
import { logger } from '@admin-frame/observability';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { ThemeProvider, ToastProvider } from '@admin-frame/ui';
import { QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { RouterProvider } from 'react-router';
import { AppErrorBoundary } from './AppErrorBoundary';
import { AuthProvider } from './auth/AuthProvider';
import { ObservabilityProvider } from './observability/ObservabilityProvider';
import { PermissionProvider } from './permission/PermissionProvider';
import { createAppRouter } from './router';

export function App({ config }: { config: AppRuntimeConfig }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        queryCache: new QueryCache({
          // 只记录诊断信息，不弹 toast、不触发重试判断（§12.2）。
          onError: (error) => {
            const appError = isAppError(error) ? error : null;
            logger.error({
              event: 'query.error',
              route: window.location.pathname,
              source: 'query',
              ...(appError?.code === undefined ? {} : { code: appError.code }),
              ...(appError?.kind === undefined ? {} : { kind: appError.kind }),
              ...(appError?.traceId === undefined ? {} : { traceId: appError.traceId }),
            });
          },
        }),
        defaultOptions: {
          queries: {
            // 仅网络错误/5xx 重试一次；中后台避免窗口聚焦误刷新（§8.3）
            retry: 1,
            staleTime: 30_000,
            refetchOnWindowFocus: false,
          },
        },
      }),
  );
  const [router] = useState(() => createAppRouter(config));

  return (
    <AppErrorBoundary>
      <ThemeProvider>
        <I18nProvider>
          <ToastProvider>
            <ObservabilityProvider config={config}>
              <QueryClientProvider client={queryClient}>
                <AuthProvider config={config}>
                  <PermissionProvider>
                    <RouterProvider router={router} />
                  </PermissionProvider>
                </AuthProvider>
              </QueryClientProvider>
            </ObservabilityProvider>
          </ToastProvider>
        </I18nProvider>
      </ThemeProvider>
    </AppErrorBoundary>
  );
}

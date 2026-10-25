import type { AppRuntimeConfig } from '@admin-frame/shared';
import { ThemeProvider, ToastProvider } from '@admin-frame/ui';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { RouterProvider } from 'react-router';
import { AppErrorBoundary } from './AppErrorBoundary';
import { AuthProvider } from './auth/AuthProvider';
import { PermissionProvider } from './permission/PermissionProvider';
import { createAppRouter } from './router';

export function App({ config }: { config: AppRuntimeConfig }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
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
        <ToastProvider>
          <QueryClientProvider client={queryClient}>
            <AuthProvider config={config}>
              <PermissionProvider>
                <RouterProvider router={router} />
              </PermissionProvider>
            </AuthProvider>
          </QueryClientProvider>
        </ToastProvider>
      </ThemeProvider>
    </AppErrorBoundary>
  );
}

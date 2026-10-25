import { Component, type ErrorInfo, type ReactNode } from 'react';
import { ErrorState } from '@admin-frame/ui';

type AppErrorBoundaryProps = {
  children: ReactNode;
};

type AppErrorBoundaryState = {
  error: Error | null;
};

/** 根错误边界：致命错误页 + 重试（§12.1）；P1 接监控上报。 */
export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  override state: AppErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): AppErrorBoundaryState {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    // 不得上报表单内容与令牌（§12.1）。
    console.error('[app] uncaught error', error, info.componentStack);
  }

  override render(): ReactNode {
    if (this.state.error) {
      return (
        <ErrorState
          title="页面加载失败"
          description="请刷新页面后重试；若持续失败请联系管理员。"
          onRetry={() => {
            window.location.reload();
          }}
        />
      );
    }
    return this.props.children;
  }
}

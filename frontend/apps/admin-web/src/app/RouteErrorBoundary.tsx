import { defaultTranslator } from '@admin-frame/i18n';
import {
  ensureTraceId,
  isChunkLoadError,
  logger,
  reloadOnceForChunkError,
  reportError,
} from '@admin-frame/observability';
import { ErrorState } from '@admin-frame/ui';
import { Component, type ReactNode } from 'react';
import { useLocation } from 'react-router';

type RouteBoundaryProps = {
  children: ReactNode;
  route: string;
};

type RouteBoundaryState = {
  error: Error | null;
  traceId: string | undefined;
  clientGenerated: boolean;
};

class RouteBoundary extends Component<RouteBoundaryProps, RouteBoundaryState> {
  override state: RouteBoundaryState = { error: null, traceId: undefined, clientGenerated: false };

  static getDerivedStateFromError(error: Error): RouteBoundaryState {
    const { traceId, clientGenerated } = ensureTraceId(error);
    return { error, traceId, clientGenerated };
  }

  override componentDidCatch(error: Error): void {
    // 不记录组件栈、表单值与令牌（§12.1）。
    logger.error({
      event: 'ui.uncaught',
      traceId: this.state.traceId,
      clientGenerated: this.state.clientGenerated,
      route: this.props.route,
      source: 'route',
    });
    reportError(error, { route: this.props.route, source: 'route' });

    // 发布后旧 chunk 失效：仅自动刷新一次，第二次提示手动刷新（§8.3）。
    if (isChunkLoadError(error) && reloadOnceForChunkError()) {
      window.location.reload();
    }
  }

  private readonly handleRetry = (): void => {
    this.setState({ error: null, traceId: undefined, clientGenerated: false });
  };

  override render(): ReactNode {
    const { error, traceId } = this.state;
    if (error) {
      return (
        <ErrorState
          title={defaultTranslator('common.pageLoadFailed')}
          description={
            isChunkLoadError(error)
              ? defaultTranslator('common.forceRefreshHint')
              : defaultTranslator('common.pageLoadFailedDescription')
          }
          traceId={traceId}
          onRetry={this.handleRetry}
        />
      );
    }
    return this.props.children;
  }
}

/** 路由级错误边界：包裹 AppLayout 的 Outlet，保留导航与重试入口（§12.1）。 */
export function RouteErrorBoundary({ children }: { children: ReactNode }) {
  const location = useLocation();
  return <RouteBoundary route={location.pathname}>{children}</RouteBoundary>;
}

import { defaultTranslator } from '@admin-frame/i18n';
import { ensureTraceId, logger, reportError } from '@admin-frame/observability';
import { ErrorState } from '@admin-frame/ui';
import { Component, type ReactNode } from 'react';

type AppErrorBoundaryProps = {
  children: ReactNode;
};

type AppErrorBoundaryState = {
  error: Error | null;
  traceId: string | undefined;
  clientGenerated: boolean;
};

/** 根错误边界：致命错误页 + 重试（§12.1）；异常经统一 logger 与上报记录。 */
export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  override state: AppErrorBoundaryState = {
    error: null,
    traceId: undefined,
    clientGenerated: false,
  };

  static getDerivedStateFromError(error: Error): AppErrorBoundaryState {
    const { traceId, clientGenerated } = ensureTraceId(error);
    return { error, traceId, clientGenerated };
  }

  override componentDidCatch(error: Error): void {
    // 不得上报表单内容与令牌（§12.1）。
    logger.error({
      event: 'ui.uncaught',
      traceId: this.state.traceId,
      clientGenerated: this.state.clientGenerated,
      source: 'root',
    });
    reportError(error, { source: 'root' });
  }

  override render(): ReactNode {
    if (this.state.error) {
      return (
        <ErrorState
          title={defaultTranslator('common.pageLoadFailed')}
          description={defaultTranslator('common.pageLoadFailedDescription')}
          traceId={this.state.traceId}
          onRetry={() => {
            window.location.reload();
          }}
        />
      );
    }
    return this.props.children;
  }
}

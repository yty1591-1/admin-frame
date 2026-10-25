import { App as AntApp, Typography } from 'antd';
import { createContext, useContext, useMemo, type ReactNode } from 'react';

export type ToastErrorInput = {
  message: string;
  /** 失败提示必须携带可复制的 trace_id（§3.6）。 */
  traceId?: string | undefined;
};

export type ToastApi = {
  success: (message: string) => void;
  error: (input: ToastErrorInput) => void;
};

const ToastContext = createContext<ToastApi | null>(null);

function ToastBridge({ children }: { children: ReactNode }) {
  const { message } = AntApp.useApp();
  const api = useMemo<ToastApi>(
    () => ({
      success: (text) => {
        void message.success(text);
      },
      error: ({ message: text, traceId }) => {
        const content =
          traceId === undefined ? (
            text
          ) : (
            <div>
              <div>{text}</div>
              <Typography.Text copyable={{ text: traceId }} style={{ fontSize: 12 }}>
                trace_id: {traceId}
              </Typography.Text>
            </div>
          );
        void message.error(content, 6);
      },
    }),
    [message],
  );

  return <ToastContext.Provider value={api}>{children}</ToastContext.Provider>;
}

/** 全局通知唯一出口（§3.6）；页面禁止直接使用 antd message/notification。 */
export function ToastProvider({ children }: { children: ReactNode }) {
  return (
    <AntApp>
      <ToastBridge>{children}</ToastBridge>
    </AntApp>
  );
}

export function useToast(): ToastApi {
  const value = useContext(ToastContext);
  if (!value) {
    throw new Error('useToast 必须在 ToastProvider 内使用');
  }
  return value;
}

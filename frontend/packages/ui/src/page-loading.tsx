import { Spin } from 'antd';

export type PageLoadingProps = {
  tip?: string | undefined;
};

/** 路由级加载态；作为 Suspense fallback 使用。 */
export function PageLoading({ tip = '加载中…' }: PageLoadingProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 12,
        minHeight: 240,
      }}
    >
      <Spin size="large" />
      <span>{tip}</span>
    </div>
  );
}

import { configureLogger, initObservability } from '@admin-frame/observability';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { useEffect, type ReactNode } from 'react';

const OTel_LOAD_DELAY_MS = 1_000;

/**
 * 可观测装配（S3 / §3.4、§5.3）：配置 logger；首屏渲染后延迟初始化 OTel。
 * OTel SDK 全部在 observability 包内动态导入，不进入入口 chunk；dsn 为空时零请求。
 */
export function ObservabilityProvider({
  config,
  children,
}: {
  config: AppRuntimeConfig;
  children: ReactNode;
}) {
  const { channel, env, monitoring } = config;

  useEffect(() => {
    configureLogger();
    if (monitoring.dsn === null) {
      return;
    }
    const timer = window.setTimeout(() => {
      initObservability({
        endpoint: monitoring.dsn,
        sampleRate: monitoring.sampleRate,
        release: monitoring.release ?? __APP_VERSION__,
        channel,
        env,
      });
    }, OTel_LOAD_DELAY_MS);
    return () => {
      window.clearTimeout(timer);
    };
  }, [channel, env, monitoring]);

  return <>{children}</>;
}

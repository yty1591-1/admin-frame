import { initPreferences } from '@vben/preferences';
import { unmountGlobalLoading } from '@vben/utils';

export interface InitApplicationOptions {
  /** 应用启动器（应用侧注入自己的 bootstrap） */
  bootstrap: (namespace: string) => Promise<void> | void;
  /** 历史缓存迁移（可选，应用侧提供） */
  migrateLegacyBrandAssets?: () => void;
  /** 应用偏好覆盖 */
  overrides: Record<string, unknown>;
}

/**
 * 统一的启动流程：偏好初始化 → 历史缓存迁移 → 应用引导 → 卸载全局 loading。
 *
 * 两端 main.ts 只保留一行 await，避免 A/B 端启动路径漂移。
 */
export async function initApplication({
  bootstrap,
  migrateLegacyBrandAssets,
  overrides,
}: InitApplicationOptions): Promise<void> {
  const env = import.meta.env.PROD ? 'prod' : 'dev';
  const appVersion = import.meta.env.VITE_APP_VERSION;
  const namespace = `${import.meta.env.VITE_APP_NAMESPACE}-${appVersion}-${env}`;

  await initPreferences({ namespace, overrides });
  migrateLegacyBrandAssets?.();
  await bootstrap(namespace);
  unmountGlobalLoading();
}

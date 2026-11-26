import type { Router } from 'vue-router';

import { fetchInstallStatus } from './install';

/** 配置页进入前：未 ready 则跳转安装向导。 */
export async function redirectIfInstallRequired(
  router: Router,
): Promise<boolean> {
  try {
    const state = await fetchInstallStatus();
    if (state.status && state.status !== 'ready') {
      await router.replace('/setup/install');
      return true;
    }
  } catch {
    // config-api 不可达时不阻断（本机可能未起）；由业务 readyz 另行门禁。
  }
  return false;
}

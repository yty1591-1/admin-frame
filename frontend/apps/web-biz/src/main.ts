import { initApplication } from '@vben/app-kernel/main';

import { bootstrap } from './bootstrap';
import { migrateLegacyBrandAssets, overridesPreferences } from './preferences';

/**
 * 应用初始化完成之后再进行页面加载渲染（初始化流程统一在内核）。
 */
await initApplication({
  bootstrap,
  migrateLegacyBrandAssets,
  overrides: overridesPreferences,
});

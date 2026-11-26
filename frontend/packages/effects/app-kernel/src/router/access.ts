import type {
  ComponentRecordType,
  GenerateMenuAndRoutesOptions,
} from '@vben/types';

import { generateAccessible } from '@vben/access';
import { $t } from '@vben/locales';
import { preferences } from '@vben/preferences';

import { message } from 'ant-design-vue';

export interface AccessGeneratorOptions {
  /** 拉取当前用户的菜单（各应用注入自己的 API） */
  fetchMenuList: () => Promise<unknown[]>;
  /** 无权限时的 403 页面（各应用注入） */
  forbiddenComponent?: () => Promise<unknown>;
  /** 布局映射（BasicLayout / IFrameView 等） */
  layoutMap: ComponentRecordType;
  /** 页面映射（各应用注入 import.meta.glob 结果） */
  pageMap: ComponentRecordType;
}

/**
 * 生成权限路由生成器：把“菜单拉取、页面映射、布局映射”留在应用侧，
 * 生成逻辑（accessMode、403、菜单 loading）收敛到内核。
 */
export function createAccessGenerator({
  fetchMenuList,
  forbiddenComponent,
  layoutMap,
  pageMap,
}: AccessGeneratorOptions) {
  return async (options: GenerateMenuAndRoutesOptions) => {
    return await generateAccessible(preferences.app.accessMode, {
      ...options,
      fetchMenuListAsync: async () => {
        message.loading({
          content: `${$t('common.loadingMenu')}...`,
          duration: 1.5,
        });
        return (await fetchMenuList()) as never;
      },
      forbiddenComponent,
      layoutMap,
      pageMap,
    });
  };
}

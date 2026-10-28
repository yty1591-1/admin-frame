import { computed } from 'vue';

import { preferences, updatePreferences } from '@vben/preferences';
import { useAccessStore, useUserStore } from '@vben/stores';

function useAccess() {
  const accessStore = useAccessStore();
  const userStore = useUserStore();
  const accessMode = computed(() => {
    return preferences.app.accessMode;
  });

  /**
   * 基于角色判断是否有权限
   * @description: Determine whether there is permission，The role is judged by the user's role
   * @param roles
   */
  function hasAccessByRoles(roles: string[]) {
    const userRoleSet = new Set(userStore.userRoles);
    const intersection = roles.filter((item) => userRoleSet.has(item));
    return intersection.length > 0;
  }

  /**
   * 基于权限码判断是否有权限
   * @description: Determine whether there is permission，The permission code is judged by the user's permission code
   * @param codes
   */
  function hasAccessByCodes(codes: string[]) {
    const userCodesSet = new Set(accessStore.accessCodes);

    // 破玻璃 / 系统角色在快照中展开为 "*"；前端必须同样视作全部权限。
    if (userCodesSet.has('*')) {
      return true;
    }
    const intersection = codes.filter((item) => userCodesSet.has(item));
    return intersection.length > 0;
  }

  /**
   * 基于权限码判断是否拥有全部权限（交集）
   * @description: 空数组必须显式挡掉：every([]) 恒为 true 会让按钮永远可见
   * @param codes
   */
  function hasAllAccessByCodes(codes: string[]) {
    const userCodesSet = new Set(accessStore.accessCodes);
    if (userCodesSet.has('*')) {
      return codes.length > 0;
    }
    return codes.length > 0 && codes.every((item) => userCodesSet.has(item));
  }

  async function toggleAccessMode() {
    updatePreferences({
      app: {
        accessMode:
          preferences.app.accessMode === 'frontend' ? 'backend' : 'frontend',
      },
    });
  }

  return {
    accessMode,
    hasAccessByCodes,
    hasAccessByRoles,
    hasAllAccessByCodes,
    toggleAccessMode,
  };
}

export { useAccess };

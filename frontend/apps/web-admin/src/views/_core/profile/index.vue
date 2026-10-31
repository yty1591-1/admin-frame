<script setup lang="ts">
import type { MeView } from '#/api';

import { computed, markRaw, ref } from 'vue';

import { Profile } from '@vben/common-ui';
import { LockKeyhole, MonitorSmartphone, UserRoundPen } from '@vben/icons';
import { useUserStore } from '@vben/stores';

import { $t } from '#/locales';

import ProfileBase from './base-setting.vue';
import ProfileSecuritySetting from './security-setting.vue';
import ProfileSessionSetting from './session-setting.vue';

const userStore = useUserStore();

/** 页签名保持英文常量，标题走 i18n；改中文标题不影响路由与埋点。 */
const tabsValue = ref<string>('profile');

const STATUS_LABEL_KEYS = {
  active: 'profile.statusActive',
  disabled: 'profile.statusDisabled',
  locked: 'profile.statusLocked',
  pending: 'profile.statusPending',
} as const;

const accountStatus = ref<MeView['status']>();

const tabs = computed(() => [
  {
    label: $t('profile.tabProfile'),
    value: 'profile',
    icon: markRaw(UserRoundPen),
  },
  {
    label: $t('profile.tabSecurity'),
    value: 'security',
    icon: markRaw(LockKeyhole),
  },
  {
    label: $t('profile.tabDevices'),
    value: 'sessions',
    icon: markRaw(MonitorSmartphone),
  },
]);

const statusLabel = computed(() =>
  accountStatus.value ? $t(STATUS_LABEL_KEYS[accountStatus.value]) : '',
);

/** 资料页加载成功即同步身份卡状态，避免页签切换后再发一次 /me。 */
function handleProfileLoaded(me: MeView) {
  accountStatus.value = me.status;
  // 昵称以服务端为准：登录时写入 store 的可能是旧值，保存后也要立即反映到身份卡。
  const displayName = me.display_name || me.subject;
  const current = userStore.userInfo;
  if (current && displayName && current.realName !== displayName) {
    userStore.setUserInfo({ ...current, realName: displayName });
  }
}
</script>
<template>
  <Profile
    v-model:model-value="tabsValue"
    :status-label="statusLabel"
    :tabs="tabs"
    :title="$t('profile.title')"
    :user-info="userStore.userInfo"
  >
    <template #content>
      <ProfileBase
        v-if="tabsValue === 'profile'"
        @loaded="handleProfileLoaded"
      />
      <ProfileSecuritySetting v-if="tabsValue === 'security'" />
      <ProfileSessionSetting v-if="tabsValue === 'sessions'" />
    </template>
  </Profile>
</template>

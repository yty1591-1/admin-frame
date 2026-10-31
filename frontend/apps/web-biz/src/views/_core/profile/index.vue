<script setup lang="ts">
import { computed, markRaw, ref } from 'vue';

import { Profile } from '@vben/common-ui';
import { LockKeyhole, MonitorSmartphone, UserRoundPen } from '@vben/icons';
import { useUserStore } from '@vben/stores';

import { $t } from '#/locales';

import ProfileBase from './base-setting.vue';
import ProfileSecuritySetting from './security-setting.vue';
import ProfileSessionSetting from './session-setting.vue';

const userStore = useUserStore();

const tabsValue = ref<string>('profile');

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
</script>
<template>
  <Profile
    v-model:model-value="tabsValue"
    :tabs="tabs"
    :title="$t('profile.title')"
    :user-info="userStore.userInfo"
  >
    <template #content>
      <ProfileBase v-if="tabsValue === 'profile'" />
      <ProfileSecuritySetting v-if="tabsValue === 'security'" />
      <ProfileSessionSetting v-if="tabsValue === 'sessions'" />
    </template>
  </Profile>
</template>

<script setup lang="ts">
import type { SecuritySchemaItem } from '@vben/common-ui';

import type { MeView } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { ProfileSecuritySetting } from '@vben/common-ui';

import { Alert, Button } from 'ant-design-vue';

import { getMeApi } from '#/api';
import { $t } from '#/locales';

const loading = ref(true);
/** 资料接口不可用时整页降级为「暂不可用」，不把失败渲染成「未绑定」。 */
const unavailable = ref(false);
const me = ref<MeView | null>(null);

/**
 * 拉取当前主体资料。
 *
 * `/me` 已接通真实后端；请求失败（网络、令牌失效）时才降级，
 * 绝不把失败渲染成「未绑定」。
 */
async function loadMe() {
  loading.value = true;
  unavailable.value = false;
  try {
    me.value = await getMeApi({ skipErrorMessage: true });
  } catch {
    me.value = null;
    unavailable.value = true;
  } finally {
    loading.value = false;
  }
}

onMounted(loadMe);

const formSchema = computed((): SecuritySchemaItem[] => {
  const data = me.value;
  const unavailableDesc = $t('profile.securityProfileUnavailable');

  if (!data) {
    return [
      {
        fieldName: 'accountPassword',
        label: $t('profile.securityAccountPassword'),
        status: 'unavailable',
        description: unavailableDesc,
      },
      {
        fieldName: 'securityPhone',
        label: $t('profile.securityPhone'),
        status: 'unavailable',
        description: unavailableDesc,
      },
      {
        fieldName: 'securityQuestion',
        label: $t('profile.securityQuestion'),
        status: 'unavailable',
        description: $t('profile.securityNotAvailableDesc'),
      },
      {
        fieldName: 'securityEmail',
        label: $t('profile.securityEmail'),
        status: 'unavailable',
        description: $t('profile.securityNotAvailableDesc'),
      },
      {
        fieldName: 'securityMfa',
        label: $t('profile.mfaDevice'),
        status: 'unavailable',
        description: $t('profile.securityMfaUnavailableDesc'),
      },
    ];
  }

  return [
    {
      fieldName: 'accountPassword',
      label: $t('profile.securityAccountPassword'),
      // B 端没有自助改密端点：即使 /me 能返回 has_password，也不给操作入口。
      status: 'unavailable',
      description: $t('profile.securityPasswordUnavailableDesc'),
    },
    {
      fieldName: 'securityPhone',
      label: $t('profile.securityPhone'),
      status: data.phone_bound ? 'bound' : 'unbound',
      description: data.phone_bound
        ? $t('profile.securityPhoneBoundDesc')
        : $t('profile.securityPhoneUnboundDesc'),
    },
    {
      fieldName: 'securityQuestion',
      label: $t('profile.securityQuestion'),
      status: 'unavailable',
      description: $t('profile.securityNotAvailableDesc'),
    },
    {
      fieldName: 'securityEmail',
      label: $t('profile.securityEmail'),
      status: 'unavailable',
      description: $t('profile.securityNotAvailableDesc'),
    },
    {
      fieldName: 'securityMfa',
      label: $t('profile.mfaDevice'),
      status: 'unavailable',
      description: $t('profile.securityMfaUnavailableDesc'),
    },
  ];
});
</script>
<template>
  <div class="space-y-5">
    <div
      v-if="unavailable"
      class="flex flex-col items-start gap-3 rounded-xl border border-dashed p-5 sm:flex-row sm:items-center sm:justify-between"
    >
      <Alert
        class="w-full flex-1"
        :message="$t('profile.securityProfileUnavailable')"
        show-icon
        type="info"
      />
      <Button class="shrink-0" @click="loadMe">
        {{ $t('profile.securityRetry') }}
      </Button>
    </div>

    <ProfileSecuritySetting :form-schema="formSchema" :loading="loading" />
  </div>
</template>

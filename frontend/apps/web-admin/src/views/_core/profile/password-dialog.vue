<script setup lang="ts">
import type { VbenFormSchema } from '@vben/app-kernel/adapter/form';
import type { Recordable } from '@vben/types';

import { computed } from 'vue';

import { ProfilePasswordSetting, z } from '@vben/common-ui';
import { useAccessStore } from '@vben/stores';

import { message, Modal } from 'ant-design-vue';

import { changePasswordApi } from '#/api';
import { $t } from '#/locales';
import { useAuthStore } from '#/store';

/** 弹窗开关由安全设置页控制；关闭即卸载表单，避免下次打开残留旧密码。 */
const open = defineModel<boolean>('open', { default: false });

const formSchema = computed((): VbenFormSchema[] => [
  {
    fieldName: 'oldPassword',
    label: $t('profile.passwordOld'),
    component: 'VbenInputPassword',
    componentProps: {
      placeholder: $t('profile.passwordOldPlaceholder'),
    },
  },
  {
    fieldName: 'newPassword',
    label: $t('profile.passwordNew'),
    component: 'VbenInputPassword',
    componentProps: {
      passwordStrength: true,
      placeholder: $t('profile.passwordNewPlaceholder'),
    },
  },
  {
    fieldName: 'confirmPassword',
    label: $t('profile.passwordConfirm'),
    component: 'VbenInputPassword',
    componentProps: {
      passwordStrength: true,
      placeholder: $t('profile.passwordConfirmPlaceholder'),
    },
    dependencies: {
      rules(values) {
        const { newPassword } = values;
        return z
          .string({ error: $t('profile.passwordConfirmRequired') })
          .min(1, { message: $t('profile.passwordConfirmRequired') })
          .refine((value) => value === newPassword, {
            message: $t('profile.passwordMismatch'),
          });
      },
      triggerFields: ['newPassword'],
    },
  },
]);

const accessStore = useAccessStore();
const authStore = useAuthStore();

/**
 * 自助改密：成功后服务端递增 token_version 并撤销全部会话，
 * 因此这里必须立即登出，避免继续使用已失效的令牌。
 */
async function handleSubmit(values: Recordable<any>) {
  try {
    await changePasswordApi({
      accessToken: accessStore.accessToken,
      newPassword: values.newPassword,
      oldPassword: values.oldPassword,
    });
    message.success($t('profile.passwordChangeSuccess'));
    open.value = false;
    await authStore.logout(false);
  } catch (error) {
    // 旧密码错误统一返回 40100；该请求走 baseRequestClient，不会自动提示。
    const backendMessage = (
      error as { response?: { data?: { message?: string } } }
    )?.response?.data?.message;
    message.error(backendMessage || $t('profile.passwordChangeFailed'));
  }
}
</script>
<template>
  <Modal
    v-model:open="open"
    destroy-on-close
    :footer="null"
    :title="$t('profile.passwordDialogTitle')"
    width="min(460px, 92vw)"
  >
    <p class="text-muted-foreground mb-4 text-sm">
      {{ $t('profile.passwordDialogHint') }}
    </p>
    <ProfilePasswordSetting :form-schema="formSchema" @submit="handleSubmit" />
  </Modal>
</template>

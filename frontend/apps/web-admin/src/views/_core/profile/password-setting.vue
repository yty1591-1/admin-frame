<script setup lang="ts">
import type { Recordable } from '@vben/types';

import type { VbenFormSchema } from '#/adapter/form';

import { computed } from 'vue';

import { ProfilePasswordSetting, z } from '@vben/common-ui';
import { useAccessStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import { changePasswordApi } from '#/api';
import { $t } from '#/locales';
import { useAuthStore } from '#/store';

const formSchema = computed((): VbenFormSchema[] => {
  return [
    {
      fieldName: 'oldPassword',
      label: '旧密码',
      component: 'VbenInputPassword',
      componentProps: {
        placeholder: '请输入旧密码',
      },
    },
    {
      fieldName: 'newPassword',
      label: '新密码',
      component: 'VbenInputPassword',
      componentProps: {
        passwordStrength: true,
        placeholder: '请输入新密码',
      },
    },
    {
      fieldName: 'confirmPassword',
      label: '确认密码',
      component: 'VbenInputPassword',
      componentProps: {
        passwordStrength: true,
        placeholder: '请再次输入新密码',
      },
      dependencies: {
        rules(values) {
          const { newPassword } = values;
          return z
            .string({ error: '请再次输入新密码' })
            .min(1, { message: '请再次输入新密码' })
            .refine((value) => value === newPassword, {
              message: '两次输入的密码不一致',
            });
        },
        triggerFields: ['newPassword'],
      },
    },
  ];
});

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
    await authStore.logout(false);
  } catch (error) {
    // 旧密码错误统一返回 40100，走 baseRequestClient 不会自动提示
    const backendMessage = (
      error as { response?: { data?: { message?: string } } }
    )?.response?.data?.message;
    message.error(backendMessage || $t('profile.passwordChangeFailed'));
  }
}
</script>
<template>
  <ProfilePasswordSetting
    class="w-1/3"
    :form-schema="formSchema"
    @submit="handleSubmit"
  />
</template>

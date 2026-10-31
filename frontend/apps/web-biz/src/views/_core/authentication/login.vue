<script lang="ts" setup>
import type { VbenFormSchema } from '@vben/common-ui';
import type { Recordable } from '@vben/types';

import { computed } from 'vue';

import { AuthenticationLogin, z } from '@vben/common-ui';
import { $t } from '@vben/locales';

import { message } from 'ant-design-vue';

import { useAuthStore } from '#/store';

defineOptions({ name: 'Login' });

const authStore = useAuthStore();

/**
 * 登录表单
 *
 * 字段保持 username / password（Vben UI 模型），由 api 层映射为后端的 subject。
 * B 端登录没有 MFA / 改密中间态：成功即跳转，失败按后端 message 提示。
 */
const formSchema = computed((): VbenFormSchema[] => {
  return [
    {
      component: 'VbenInput',
      componentProps: {
        placeholder: $t('authentication.usernameTip'),
      },
      fieldName: 'username',
      label: $t('authentication.username'),
      rules: z.string().min(1, { message: $t('authentication.usernameTip') }),
    },
    {
      component: 'VbenInputPassword',
      componentProps: {
        placeholder: $t('authentication.password'),
      },
      fieldName: 'password',
      label: $t('authentication.password'),
      rules: z.string().min(1, { message: $t('authentication.passwordTip') }),
    },
  ];
});

/**
 * 登录提交。
 *
 * 登录走无拦截器的 baseRequestClient（避免 401 触发刷新/重认证递归），
 * 因此错误提示必须在这里兜底：优先后端 message，其次抛出的 Error.message。
 */
async function handleSubmit(values: Recordable<any>) {
  try {
    await authStore.authLogin(values);
  } catch (error) {
    const backendMessage = (
      error as { response?: { data?: { message?: string } } }
    )?.response?.data?.message;
    const fallback = (error as { message?: string })?.message;
    if (backendMessage || fallback) {
      message.error(backendMessage || fallback);
    }
  }
}
</script>

<template>
  <AuthenticationLogin
    :form-schema="formSchema"
    :loading="authStore.loginLoading"
    :show-code-login="false"
    :show-forget-password="false"
    :show-qrcode-login="false"
    :show-register="false"
    :show-third-party-login="false"
    @submit="handleSubmit"
  />
</template>

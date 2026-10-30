<script lang="ts" setup>
import type { VbenFormSchema } from '@vben/common-ui';
import type { Recordable } from '@vben/types';

import type { LoginOutcome, MfaSetupData } from '#/api/core/auth';

import { computed, onUnmounted, ref } from 'vue';

import { AuthenticationLogin, z } from '@vben/common-ui';
import { $t } from '@vben/locales';

import { message } from 'ant-design-vue';

import { useAuthStore } from '#/store';

import MfaBinding from './mfa-binding.vue';
import MfaVerify from './mfa-verify.vue';
import PasswordChange from './password-change.vue';

defineOptions({ name: 'Login' });

type LoginStep =
  | 'mfa-binding'
  | 'mfa-challenge'
  | 'password'
  | 'password-change';

const authStore = useAuthStore();

const step = ref<LoginStep>('password');
const challengeTicket = ref('');
const passwordChangeTicket = ref('');
const setupTicket = ref('');
const setupData = ref<MfaSetupData>();
const errorText = ref('');
const cooldown = ref(0);

let cooldownTimer: ReturnType<typeof setInterval> | undefined;

/**
 * 登录表单
 *
 * 字段保持 username / password（Vben UI 模型），由 api 层映射为后端的 subject。
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

function stopCooldown() {
  if (cooldownTimer) {
    clearInterval(cooldownTimer);
    cooldownTimer = undefined;
  }
  cooldown.value = 0;
}

function startCooldown(seconds: number) {
  stopCooldown();
  cooldown.value = seconds;
  cooldownTimer = setInterval(() => {
    cooldown.value -= 1;
    if (cooldown.value <= 0) {
      stopCooldown();
    }
  }, 1000);
}

/**
 * 统一的登录错误处理。
 *
 * 429 按 Retry-After 倒计时；MFA 票据类错误（400/401）说明票据已被消费，
 * 必须退回密码登录，不能原地重试。
 */
function applyLoginError(error: unknown, invalidateTicket = false) {
  const response = (
    error as {
      message?: string;
      response?: {
        data?: { message?: string };
        headers?: Record<string, string>;
        status?: number;
      };
    }
  )?.response;
  const status = response?.status;
  const retryAfter = Number(response?.headers?.['retry-after'] ?? 0);
  const backendMessage = response?.data?.message;

  if (status === 429 && retryAfter > 0) {
    startCooldown(retryAfter);
    if (backendMessage) {
      message.error(backendMessage);
    }
    return;
  }
  if (invalidateTicket && (status === 400 || status === 401)) {
    errorText.value = $t('authentication.mfaTicketExpired');
    return;
  }
  const fallback = (error as { message?: string })?.message;
  if (backendMessage || fallback) {
    message.error(backendMessage || fallback);
  }
}

function resetToPassword() {
  step.value = 'password';
  challengeTicket.value = '';
  passwordChangeTicket.value = '';
  setupTicket.value = '';
  setupData.value = undefined;
  errorText.value = '';
  stopCooldown();
}

async function handlePasswordSubmit(values: Recordable<any>) {
  errorText.value = '';
  try {
    const outcome = await authStore.authLogin(values);
    await advanceStep(outcome);
  } catch (error) {
    applyLoginError(error);
  }
}

/** 中间态统一路由：改密 → MFA 挑战 / 强制绑定；拿到令牌时 store 已完成跳转。 */
async function advanceStep(outcome: LoginOutcome) {
  if (outcome.kind === 'password-change') {
    passwordChangeTicket.value = outcome.ticket;
    step.value = 'password-change';
    return;
  }
  if (outcome.kind === 'mfa-challenge') {
    challengeTicket.value = outcome.ticket;
    step.value = 'mfa-challenge';
    return;
  }
  if (outcome.kind === 'mfa-binding') {
    setupTicket.value = outcome.ticket;
    setupData.value = await authStore.startMfaBinding(outcome.ticket);
    step.value = 'mfa-binding';
  }
}

async function handlePasswordChangeSubmit(newPassword: string) {
  errorText.value = '';
  try {
    const outcome = await authStore.completePasswordChange(
      passwordChangeTicket.value,
      newPassword,
    );
    await advanceStep(outcome);
  } catch (error) {
    // 改密票据一次性，400/401 说明票据已失效，只能退回密码登录
    applyLoginError(error, true);
  }
}

async function handleMfaSubmit(code: string) {
  errorText.value = '';
  try {
    await authStore.verifyMfaChallenge(challengeTicket.value, code);
  } catch (error) {
    applyLoginError(error, true);
  }
}

async function handleBindingSubmit(code: string) {
  errorText.value = '';
  try {
    await authStore.confirmMfaBinding(setupTicket.value, code);
  } catch (error) {
    applyLoginError(error, true);
  }
}

onUnmounted(stopCooldown);
</script>

<template>
  <AuthenticationLogin
    v-if="step === 'password'"
    :form-schema="formSchema"
    :loading="authStore.loginLoading"
    :show-code-login="false"
    :show-forget-password="false"
    :show-qrcode-login="false"
    :show-register="false"
    :show-third-party-login="false"
    @submit="handlePasswordSubmit"
  />

  <PasswordChange
    v-else-if="step === 'password-change'"
    :error-text="errorText"
    :loading="authStore.loginLoading"
    @back="resetToPassword"
    @submit="handlePasswordChangeSubmit"
  />

  <MfaVerify
    v-else-if="step === 'mfa-challenge'"
    :cooldown="cooldown"
    :error-text="errorText"
    :loading="authStore.loginLoading"
    @back="resetToPassword"
    @submit="handleMfaSubmit"
  />

  <MfaBinding
    v-else-if="step === 'mfa-binding' && setupData"
    :cooldown="cooldown"
    :error-text="errorText"
    :loading="authStore.loginLoading"
    :setup="setupData"
    @back="resetToPassword"
    @submit="handleBindingSubmit"
  />
</template>

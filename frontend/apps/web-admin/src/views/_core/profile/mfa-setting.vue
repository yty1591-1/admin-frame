<script lang="ts" setup>
import type { MfaSetupData } from '#/api/core/auth';

import { computed, onMounted, ref, watch } from 'vue';

import { Alert, Button, Input, message, Modal, Spin } from 'ant-design-vue';
import QRCode from 'qrcode';

import { getMeApi } from '#/api';
import { $t } from '#/locales';
import { useAuthStore } from '#/store';

const authStore = useAuthStore();

const loading = ref(true);
const mfaEnabled = ref(false);

const setupVisible = ref(false);
const setupLoading = ref(false);
const setupData = ref<MfaSetupData>();
const qrDataUrl = ref('');

const bindCode = ref('');
const bindError = ref('');
const binding = ref(false);

const unbindVisible = ref(false);
const unbindCode = ref('');
const unbindError = ref('');
const unbinding = ref(false);

/** 从统一错误信封中提取可展示文案，缺省回退调用方提供的默认值。 */
function errorMessageOf(error: unknown, fallback: string) {
  const response = (error as { response?: { data?: { message?: string } } })
    ?.response;
  return response?.data?.message || fallback;
}

// 首码确认只接受 TOTP：后端不允许用恢复码完成首次绑定。
watch(bindCode, (value) => {
  const next = value.replaceAll(/\D/g, '').slice(0, 6);
  if (next !== value) {
    bindCode.value = next;
  }
});

// 解绑接受动态码或一次性恢复码，因此放行字母与连字符。
watch(unbindCode, (value) => {
  const next = value
    .replaceAll(/[^0-9A-Za-z-]/g, '')
    .toUpperCase()
    .slice(0, 19);
  if (next !== value) {
    unbindCode.value = next;
  }
});

const canBind = computed(() => {
  return /^\d{6}$/.test(bindCode.value) && !binding.value;
});

const canUnbind = computed(() => {
  return unbindCode.value.replaceAll('-', '').length >= 6 && !unbinding.value;
});

async function loadStatus() {
  loading.value = true;
  try {
    const me = await getMeApi();
    mfaEnabled.value = me.mfa_enabled;
  } catch {
    // 状态加载失败时保持未绑定展示，避免误判
  } finally {
    loading.value = false;
  }
}

onMounted(loadStatus);

async function openBind() {
  setupLoading.value = true;
  bindError.value = '';
  try {
    const data = await authStore.startMfaBindingForAccount();
    setupData.value = data;
    qrDataUrl.value = await QRCode.toDataURL(data.otpauth_uri, {
      margin: 1,
      width: 220,
    });
    bindCode.value = '';
    setupVisible.value = true;
  } catch {
    // 绑定初始化走 requestClient，错误提示由请求拦截器统一展示
  } finally {
    setupLoading.value = false;
  }
}

async function confirmBind() {
  if (!canBind.value) {
    return;
  }
  binding.value = true;
  bindError.value = '';
  try {
    await authStore.confirmMfaBindingForAccount(bindCode.value);
    setupVisible.value = false;
    message.success($t('profile.mfaBindSuccess'));
    await loadStatus();
  } catch (error) {
    bindError.value = errorMessageOf(error, $t('profile.mfaCodeInvalid'));
  } finally {
    binding.value = false;
  }
}

function openUnbind() {
  unbindCode.value = '';
  unbindError.value = '';
  unbindVisible.value = true;
}

async function confirmUnbind() {
  if (!canUnbind.value) {
    return;
  }
  unbinding.value = true;
  unbindError.value = '';
  try {
    // 解绑成功后服务端撤销全部会话，store 内会同步登出并跳转登录页
    await authStore.disableMfaForAccount(unbindCode.value);
    unbindVisible.value = false;
  } catch (error) {
    unbindError.value = errorMessageOf(error, $t('profile.mfaUnbindFailed'));
  } finally {
    unbinding.value = false;
  }
}

function downloadRecoveryCodes() {
  if (!setupData.value) {
    return;
  }
  const content = setupData.value.recovery_codes
    .map((item, index) => `${index + 1}. ${item}`)
    .join('\n');
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.download = 'mfa-recovery-codes.txt';
  anchor.href = url;
  anchor.click();
  URL.revokeObjectURL(url);
}
</script>

<template>
  <div class="rounded-lg border p-4">
    <div class="flex flex-row items-center justify-between">
      <div class="space-y-0.5">
        <p class="text-base font-medium">{{ $t('profile.mfaDevice') }}</p>
        <p class="text-muted-foreground text-sm">
          <Spin v-if="loading" size="small" />
          <template v-else>
            {{
              mfaEnabled
                ? $t('profile.mfaBoundDesc')
                : $t('profile.mfaUnboundDesc')
            }}
          </template>
        </p>
      </div>
      <Button v-if="mfaEnabled" :disabled="loading" danger @click="openUnbind">
        {{ $t('profile.mfaUnbind') }}
      </Button>
      <Button
        v-else
        :disabled="loading"
        :loading="setupLoading"
        type="primary"
        @click="openBind"
      >
        {{ $t('profile.mfaBind') }}
      </Button>
    </div>

    <Modal
      v-model:open="setupVisible"
      :confirm-loading="binding"
      :ok-button-props="{ disabled: !canBind }"
      :width="460"
      :cancel-text="$t('common.cancel')"
      :ok-text="$t('profile.mfaBindConfirm')"
      :title="$t('profile.mfaBindTitle')"
      @ok="confirmBind"
    >
      <Alert
        v-if="bindError"
        :message="bindError"
        class="mb-3"
        show-icon
        type="error"
      />
      <div v-if="setupData" class="space-y-4">
        <div class="bg-muted rounded-lg p-4">
          <p class="text-muted-foreground mb-3 text-center text-xs">
            {{ $t('profile.mfaBindScanTip') }}
          </p>
          <img
            v-if="qrDataUrl"
            :src="qrDataUrl"
            alt="otpauth"
            class="mx-auto h-40 w-40"
          />
          <div class="mt-3 text-center">
            <span class="text-muted-foreground text-xs">
              {{ $t('profile.mfaSecret') }}
            </span>
            <p class="text-foreground mt-1 font-mono text-xs break-all">
              {{ setupData.secret }}
            </p>
          </div>
        </div>

        <div class="bg-muted rounded-lg p-4">
          <div class="mb-2 flex items-center justify-between gap-2">
            <span class="text-muted-foreground text-xs">
              {{ $t('profile.mfaRecoveryCodes') }}
            </span>
            <Button size="small" type="link" @click="downloadRecoveryCodes">
              {{ $t('profile.mfaDownloadRecoveryCodes') }}
            </Button>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <span
              v-for="item in setupData.recovery_codes"
              :key="item"
              class="text-foreground bg-card rounded px-2 py-1 font-mono text-xs"
            >
              {{ item }}
            </span>
          </div>
        </div>

        <Input
          v-model:value="bindCode"
          :disabled="!!bindError"
          :maxlength="6"
          :placeholder="$t('profile.mfaCodePlaceholder')"
          autocomplete="one-time-code"
          class="text-center text-lg tracking-[0.5em]"
          inputmode="numeric"
          size="large"
          @press-enter="confirmBind"
        />
      </div>
    </Modal>

    <Modal
      v-model:open="unbindVisible"
      :confirm-loading="unbinding"
      :ok-button-props="{ disabled: !canUnbind }"
      :cancel-text="$t('common.cancel')"
      :ok-text="$t('profile.mfaUnbindConfirm')"
      :title="$t('profile.mfaUnbindTitle')"
      @ok="confirmUnbind"
    >
      <Alert
        v-if="unbindError"
        :message="unbindError"
        class="mb-3"
        show-icon
        type="error"
      />
      <p class="text-muted-foreground mb-3 text-sm">
        {{ $t('profile.mfaUnbindWarning') }}
      </p>
      <Input
        v-model:value="unbindCode"
        :disabled="!!unbindError"
        :maxlength="19"
        :placeholder="$t('profile.mfaUnbindCodePlaceholder')"
        autocomplete="off"
        class="text-center text-base tracking-wider"
        size="large"
        @press-enter="confirmUnbind"
      />
      <p class="text-muted-foreground mt-2 text-xs">
        {{ $t('profile.mfaUnbindCodeTip') }}
      </p>
    </Modal>
  </div>
</template>

<script lang="ts" setup>
import type { MfaSetupData } from '#/api/core/auth';

import { computed, onMounted, ref, watch } from 'vue';

import { $t } from '@vben/locales';

import { Alert, Button, Input } from 'ant-design-vue';
import QRCode from 'qrcode';

interface Props {
  cooldown?: number;
  errorText?: string;
  loading?: boolean;
  setup: MfaSetupData;
}

const props = withDefaults(defineProps<Props>(), {
  cooldown: 0,
  errorText: '',
  loading: false,
});

const emit = defineEmits<{
  back: [];
  submit: [string];
}>();

const code = ref('');
const qrDataUrl = ref('');

watch(code, (value) => {
  const next = value.replaceAll(/\D/g, '').slice(0, 6);
  if (next !== value) {
    code.value = next;
  }
});

const canSubmit = computed(() => {
  return (
    /^\d{6}$/.test(code.value) &&
    !props.loading &&
    !props.errorText &&
    props.cooldown <= 0
  );
});

onMounted(async () => {
  qrDataUrl.value = await QRCode.toDataURL(props.setup.otpauth_uri, {
    margin: 1,
    width: 220,
  });
});

function handleSubmit() {
  if (!canSubmit.value) {
    return;
  }
  emit('submit', code.value);
}

function downloadRecoveryCodes() {
  const content = props.setup.recovery_codes
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
  <div>
    <h2 class="text-foreground text-xl font-medium">
      {{ $t('authentication.mfaBindingTitle') }}
    </h2>
    <p class="text-muted-foreground mt-2 mb-6 text-sm">
      {{ $t('authentication.mfaBindingSubtitle') }}
    </p>

    <Alert
      v-if="errorText"
      :message="errorText"
      class="mb-4"
      show-icon
      type="error"
    />

    <div class="bg-muted mb-4 rounded-lg p-4">
      <p class="text-muted-foreground mb-3 text-center text-xs">
        {{ $t('authentication.mfaScanTip') }}
      </p>
      <img
        v-if="qrDataUrl"
        :src="qrDataUrl"
        alt="otpauth"
        class="mx-auto h-40 w-40"
      />
      <div class="mt-3 text-center">
        <span class="text-muted-foreground text-xs">
          {{ $t('authentication.mfaSecret') }}
        </span>
        <p class="text-foreground mt-1 font-mono text-xs break-all">
          {{ setup.secret }}
        </p>
      </div>
    </div>

    <div class="bg-muted mb-4 rounded-lg p-4">
      <div class="mb-2 flex items-center justify-between gap-2">
        <span class="text-muted-foreground text-xs">
          {{ $t('authentication.mfaRecoveryCodes') }}
        </span>
        <Button size="small" type="link" @click="downloadRecoveryCodes">
          {{ $t('authentication.mfaDownloadRecoveryCodes') }}
        </Button>
      </div>
      <div class="grid grid-cols-2 gap-2">
        <span
          v-for="item in setup.recovery_codes"
          :key="item"
          class="text-foreground bg-card rounded px-2 py-1 font-mono text-xs"
        >
          {{ item }}
        </span>
      </div>
    </div>

    <Input
      v-model:value="code"
      :disabled="!!errorText"
      :maxlength="6"
      :placeholder="$t('authentication.mfaCodeTip')"
      autocomplete="one-time-code"
      class="text-center text-lg tracking-[0.5em]"
      inputmode="numeric"
      size="large"
      @press-enter="handleSubmit"
    />

    <Button
      :disabled="!canSubmit"
      :loading="loading"
      block
      class="mt-4"
      size="large"
      type="primary"
      @click="handleSubmit"
    >
      {{
        cooldown > 0 ? `${cooldown}s` : $t('authentication.mfaConfirmBinding')
      }}
    </Button>

    <Button block class="mt-3" type="link" @click="emit('back')">
      {{ $t('authentication.mfaBackToLogin') }}
    </Button>
  </div>
</template>

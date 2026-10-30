<script lang="ts" setup>
import { computed, ref, watch } from 'vue';

import { $t } from '@vben/locales';

import { Alert, Button, Input } from 'ant-design-vue';

interface Props {
  cooldown?: number;
  errorText?: string;
  loading?: boolean;
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

/**
 * 两种第二因素：身份验证器动态码（6 位数字）与一次性恢复码。
 *
 * 后端恢复码为 10 字节 Base32、按 4 位分组带连字符（如 ABCD-EFGH-IJKL-MNOP），
 * 因此恢复码模式必须放行字母与连字符。
 */
type CodeMode = 'recovery' | 'totp';

const code = ref('');
const mode = ref<CodeMode>('totp');

watch([code, mode], ([value, current]) => {
  const next =
    current === 'totp'
      ? value.replaceAll(/\D/g, '').slice(0, 6)
      : value
          .replaceAll(/[^0-9A-Za-z-]/g, '')
          .toUpperCase()
          .slice(0, 19);
  if (next !== value) {
    code.value = next;
  }
});

const canSubmit = computed(() => {
  const formatValid =
    mode.value === 'totp' ? /^\d{6}$/.test(code.value) : code.value.replaceAll('-', '').length >= 6;
  return formatValid && !props.loading && !props.errorText && props.cooldown <= 0;
});

function toggleMode() {
  if (props.errorText) {
    return;
  }
  mode.value = mode.value === 'totp' ? 'recovery' : 'totp';
  code.value = '';
}

function handleSubmit() {
  if (!canSubmit.value) {
    return;
  }
  emit('submit', code.value);
}
</script>

<template>
  <div>
    <h2 class="text-foreground text-xl font-medium">
      {{ $t('authentication.mfaTitle') }}
    </h2>
    <p class="text-muted-foreground mt-2 mb-6 text-sm">
      {{
        mode === 'totp'
          ? $t('authentication.mfaSubtitle')
          : $t('authentication.mfaRecoverySubtitle')
      }}
    </p>

    <Alert v-if="errorText" :message="errorText" class="mb-4" show-icon type="error" />

    <Input
      v-model:value="code"
      :disabled="!!errorText"
      :maxlength="mode === 'totp' ? 6 : 19"
      :placeholder="
        mode === 'totp' ? $t('authentication.mfaCodeTip') : $t('authentication.mfaRecoveryCodeTip')
      "
      :autocomplete="mode === 'totp' ? 'one-time-code' : 'off'"
      :class="
        mode === 'totp'
          ? 'text-center text-lg tracking-[0.5em]'
          : 'text-center text-base tracking-wider'
      "
      :inputmode="mode === 'totp' ? 'numeric' : 'text'"
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
      {{ cooldown > 0 ? `${cooldown}s` : $t('authentication.mfaVerify') }}
    </Button>

    <Button block :disabled="!!errorText" class="mt-3" type="link" @click="toggleMode">
      {{
        mode === 'totp' ? $t('authentication.mfaUseRecoveryCode') : $t('authentication.mfaUseTotp')
      }}
    </Button>

    <Button block class="mt-1" type="link" @click="emit('back')">
      {{ $t('authentication.mfaBackToLogin') }}
    </Button>
  </div>
</template>

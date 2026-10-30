<script lang="ts" setup>
import { computed, ref } from 'vue';

import { $t } from '@vben/locales';

import { Alert, Button, Input } from 'ant-design-vue';

interface Props {
  errorText?: string;
  loading?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  errorText: '',
  loading: false,
});

const emit = defineEmits<{
  back: [];
  submit: [string];
}>();

const newPassword = ref('');
const confirmPassword = ref('');

/**
 * 与后端密码策略保持一致：至少 12 位且包含大小写/数字/符号中的三类。
 *
 * 策略以服务端为准，这里只做前置提示，避免明显不合规的请求发出去。
 */
const passwordValid = computed(() => {
  const value = newPassword.value;
  if (value.length < 12) {
    return false;
  }
  const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((pattern) =>
    pattern.test(value),
  ).length;
  return classes >= 3;
});

const canSubmit = computed(() => {
  return (
    passwordValid.value &&
    newPassword.value === confirmPassword.value &&
    !props.loading &&
    !props.errorText
  );
});

function handleSubmit() {
  if (!canSubmit.value) {
    return;
  }
  emit('submit', newPassword.value);
}
</script>

<template>
  <div>
    <h2 class="text-foreground text-xl font-medium">
      {{ $t('authentication.passwordChangeTitle') }}
    </h2>
    <p class="text-muted-foreground mt-2 mb-6 text-sm">
      {{ $t('authentication.passwordChangeSubtitle') }}
    </p>

    <Alert v-if="errorText" :message="errorText" class="mb-4" show-icon type="error" />

    <div class="space-y-3">
      <div>
        <p class="text-muted-foreground mb-1 text-xs">
          {{ $t('authentication.newPassword') }}
        </p>
        <Input.Password
          v-model:value="newPassword"
          :disabled="!!errorText"
          :placeholder="$t('authentication.passwordChangeRule')"
          autocomplete="new-password"
          size="large"
          @press-enter="handleSubmit"
        />
      </div>
      <div>
        <p class="text-muted-foreground mb-1 text-xs">
          {{ $t('authentication.confirmPassword') }}
        </p>
        <Input.Password
          v-model:value="confirmPassword"
          :disabled="!!errorText"
          :placeholder="$t('authentication.confirmPasswordTip')"
          autocomplete="new-password"
          size="large"
          @press-enter="handleSubmit"
        />
      </div>
    </div>

    <p v-if="newPassword && !passwordValid" class="mt-2 text-xs text-red-500">
      {{ $t('authentication.passwordChangeRule') }}
    </p>
    <p
      v-else-if="confirmPassword && newPassword !== confirmPassword"
      class="mt-2 text-xs text-red-500"
    >
      {{ $t('authentication.confirmPasswordTip') }}
    </p>

    <Button
      :disabled="!canSubmit"
      :loading="loading"
      block
      class="mt-4"
      size="large"
      type="primary"
      @click="handleSubmit"
    >
      {{ $t('authentication.passwordChangeSubmit') }}
    </Button>

    <Button block class="mt-3" type="link" @click="emit('back')">
      {{ $t('authentication.mfaBackToLogin') }}
    </Button>
  </div>
</template>

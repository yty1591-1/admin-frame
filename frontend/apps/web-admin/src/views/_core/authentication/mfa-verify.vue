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

const code = ref('');

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
      {{ $t('authentication.mfaSubtitle') }}
    </p>

    <Alert
      v-if="errorText"
      :message="errorText"
      class="mb-4"
      show-icon
      type="error"
    />

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
      {{ cooldown > 0 ? `${cooldown}s` : $t('authentication.mfaVerify') }}
    </Button>

    <Button block class="mt-3" type="link" @click="emit('back')">
      {{ $t('authentication.mfaBackToLogin') }}
    </Button>
  </div>
</template>

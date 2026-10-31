<script setup lang="ts">
import type { Recordable } from '@vben/types';

import type { VbenFormSchema } from '@vben-core/form-ui';

import type { InfoItem } from './types';

import { computed, h, reactive } from 'vue';

import { $t } from '@vben/locales';

import { useVbenForm } from '@vben-core/form-ui';
import { VbenButton, VbenDescriptions } from '@vben-core/shadcn-ui';

interface Props {
  /**
   * 只读资料项（登录名、账号状态、端别、商户、角色等）。
   *
   * 值由页面格式化；组件只负责按 `xs:1 / sm:2 / lg:3` 响应式排布。
   */
  infoItems?: InfoItem[];
  formSchema?: VbenFormSchema[];
  /** 表单上方提示；用于说明「接口未接通」等降级原因。 */
  hint?: string;
  /** 只读模式：隐藏提交按钮（降级端不允许提交）。 */
  readonly?: boolean;
  submitting?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  formSchema: () => [],
  infoItems: () => [],
  hint: '',
  readonly: false,
  submitting: false,
});

const emit = defineEmits<{
  submit: [Recordable<any>];
}>();

const descriptionItems = computed(() =>
  props.infoItems.map((item, index) => ({
    key: item.label || index,
    label: item.label,
    // 说明文案作为第二行，窄屏时随内容自然换行，不做省略。
    content: item.description
      ? h('div', { class: 'space-y-1' }, [
          h('div', item.value || '-'),
          h(
            'div',
            { class: 'text-muted-foreground text-xs' },
            item.description,
          ),
        ])
      : item.value || '-',
  })),
);

const [Form, formApi] = useVbenForm(
  reactive({
    commonConfig: {
      // 所有表单项
      componentProps: {
        class: 'w-full',
      },
    },
    layout: 'horizontal',
    schema: computed(() => props.formSchema),
    showDefaultActions: false,
  }),
);

async function handleSubmit() {
  if (props.readonly || props.submitting) {
    return;
  }
  const { valid } = await formApi.validate();
  const values = await formApi.getValues();
  if (valid) {
    emit('submit', values);
  }
}

defineExpose({
  getFormApi: () => formApi,
});
</script>
<template>
  <div class="space-y-8">
    <!-- 只读区与可编辑区都靠「标题 + 分隔线」分层，不再嵌套第二层卡片边框。 -->
    <section v-if="descriptionItems.length > 0">
      <VbenDescriptions
        :colon="false"
        :items="descriptionItems"
        :title="$t('profile.basicInfo')"
      />
    </section>

    <!-- 表单区固定最大宽度：宽屏下输入框不被拉成整行，避免「一行一个字符」的观感。 -->
    <section v-if="formSchema.length > 0" class="border-t border-border pt-6">
      <div class="mb-4 space-y-1">
        <h3 class="text-base font-semibold">{{ $t('profile.editProfile') }}</h3>
        <p v-if="hint" class="text-muted-foreground text-sm">
          {{ hint }}
        </p>
      </div>
      <div class="max-w-md" @keydown.enter.prevent="handleSubmit">
        <Form />
        <VbenButton
          v-if="!readonly"
          :loading="submitting"
          class="mt-4 h-9 px-4"
          type="submit"
          @click="handleSubmit"
        >
          {{ $t('profile.updateProfile') }}
        </VbenButton>
      </div>
    </section>
  </div>
</template>

<script lang="ts" setup>
import type { ChangeAction } from '#/api';

import { computed, ref, watch } from 'vue';

import { Alert, Button, Input, Modal } from 'ant-design-vue';

const props = withDefaults(
  defineProps<{
    action?: ChangeAction;
    message?: string;
    open: boolean;
    payload?: Record<string, unknown>;
    submitting?: boolean;
    targetId?: string;
  }>(),
  {
    message: '数据已被他人修改，或该操作需要走审批。',
    payload: () => ({}),
    submitting: false,
  },
);

const emit = defineEmits<{
  'update:open': [boolean];
  refresh: [];
  submit: [
    {
      action: ChangeAction;
      payload: Record<string, unknown>;
      reason: string;
      targetId?: string;
    },
  ];
}>();

const reason = ref('');

watch(
  () => props.open,
  (open) => {
    if (open) {
      reason.value = '';
    }
  },
);

const payloadText = computed(() =>
  JSON.stringify(props.payload ?? {}, null, 2),
);

function close() {
  emit('update:open', false);
}

function refresh() {
  emit('refresh');
  close();
}

function submit() {
  if (!props.action) {
    return;
  }
  emit('submit', {
    action: props.action,
    payload: props.payload ?? {},
    reason: reason.value,
    targetId: props.targetId,
  });
}
</script>

<template>
  <Modal :open="open" title="保存被拒绝" @cancel="close">
    <Alert :message="message" class="mb-3" show-icon type="warning" />
    <template v-if="action">
      <p class="text-muted-foreground mb-1 text-xs">审批动作</p>
      <p class="text-foreground mb-3 font-mono text-sm">{{ action }}</p>
      <p class="text-muted-foreground mb-1 text-xs">申请载荷</p>
      <pre
        class="bg-muted mb-3 max-h-52 overflow-auto rounded p-2 font-mono text-xs"
        >{{ payloadText }}</pre>
      <p class="text-muted-foreground mb-1 text-xs">申请原因</p>
      <Input.TextArea
        v-model:value="reason"
        :rows="2"
        placeholder="请填写提交审批的原因"
      />
    </template>
    <template #footer>
      <Button @click="close">关闭</Button>
      <Button @click="refresh">刷新详情</Button>
      <Button
        v-if="action"
        :loading="submitting"
        type="primary"
        @click="submit"
      >
        提交审批单
      </Button>
    </template>
  </Modal>
</template>

<script lang="ts" setup>
import type { FormField } from './types';

import { reactive, watch } from 'vue';

import {
  DatePicker,
  Form,
  FormItem,
  Input,
  InputNumber,
  message,
  Modal,
  Select,
  Switch,
} from 'ant-design-vue';

interface Props {
  fields: FormField[];
  loading?: boolean;
  modelValue?: Record<string, any>;
  open: boolean;
  submitText?: string;
  title: string;
  width?: number;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  modelValue: () => ({}),
  submitText: '保存',
  width: 640,
});

const emit = defineEmits<{
  'update:open': [boolean];
  refresh: [];
  submit: [Record<string, any>];
}>();

const form = reactive<Record<string, any>>({});

watch(
  () => [props.open, props.modelValue],
  () => {
    if (!props.open) {
      return;
    }
    for (const key of Object.keys(form)) {
      form[key] = undefined;
    }
    Object.assign(form, JSON.parse(JSON.stringify(props.modelValue ?? {})));
  },
  { deep: true, immediate: true },
);

function close() {
  emit('update:open', false);
}

function handleOk() {
  for (const field of props.fields) {
    const value = form[field.name];
    if (
      field.required &&
      (value === undefined ||
        value === null ||
        value === '' ||
        (Array.isArray(value) && value.length === 0))
    ) {
      message.error(`请填写${field.label}`);
      return;
    }
  }
  const payload = { ...form };
  for (const field of props.fields) {
    if (field.component !== 'json') {
      continue;
    }
    const raw = payload[field.name];
    if (typeof raw !== 'string') {
      continue;
    }
    try {
      payload[field.name] = JSON.parse(raw || '{}');
    } catch {
      message.error(`${field.label} 不是合法 JSON`);
      return;
    }
  }
  emit('submit', payload);
}
</script>

<template>
  <Modal
    :confirm-loading="loading"
    :ok-text="submitText"
    :open="open"
    :title="title"
    :width="width"
    @cancel="close"
    @ok="handleOk"
  >
    <Form layout="vertical">
      <div class="grid grid-cols-2 gap-x-4">
        <FormItem
          v-for="field in fields"
          :key="field.name"
          :class="field.span === 2 ? 'col-span-2' : ''"
          :label="field.label"
          :name="field.name"
          :required="field.required"
        >
          <Input
            v-if="!field.component || field.component === 'input'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            :placeholder="field.placeholder"
          />
          <Input.TextArea
            v-else-if="field.component === 'textarea' || field.component === 'json'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            :placeholder="field.placeholder"
            :rows="field.rows ?? 4"
          />
          <InputNumber
            v-else-if="field.component === 'number'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            :max="field.max"
            :min="field.min"
            class="w-full"
          />
          <Select
            v-else-if="field.component === 'select'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            :options="field.options"
            :placeholder="field.placeholder"
            allow-clear
            class="w-full"
          />
          <Select
            v-else-if="field.component === 'tags'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            :placeholder="field.placeholder"
            class="w-full"
            mode="tags"
          />
          <Switch
            v-else-if="field.component === 'switch'"
            v-model:checked="form[field.name]"
            :disabled="field.disabled"
          />
          <DatePicker
            v-else-if="field.component === 'datetime'"
            v-model:value="form[field.name]"
            :disabled="field.disabled"
            class="w-full"
            show-time
            value-format="YYYY-MM-DDTHH:mm:ss[Z]"
          />
        </FormItem>
      </div>
    </Form>
  </Modal>
</template>

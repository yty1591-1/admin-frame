<script lang="ts" setup>
import { computed } from 'vue';

const props = defineProps<{
  value?: unknown;
}>();

const text = computed(() =>
  props.value === null || props.value === undefined ? '—' : JSON.stringify(props.value, null, 2),
);
</script>

<template>
  <pre class="bg-muted max-h-80 overflow-auto rounded p-3 font-mono text-xs whitespace-pre-wrap">{{
    text
  }}</pre>
</template>

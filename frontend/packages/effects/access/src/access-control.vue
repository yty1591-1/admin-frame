<!--
 Access control component for fine-grained access control.
 权限码支持两种判定：mode='any'（默认，任一命中）与 mode='all'（全部命中）。
 TODO: 可以扩展更完善的功能：
 1. 支持多个角色，只要有一个角色满足即可 或者 多个角色全部满足
 2. 支持自定义权限码和角色的判断逻辑
-->
<script lang="ts" setup>
import { computed } from 'vue';

import { useAccess } from './use-access';

interface Props {
  /**
   * Specified codes is visible
   * @default []
   */
  codes?: string[];

  /**
   * 通过什么方式来控制组件，如果是 role，则传入角色，如果是 code，则传入权限码
   * @default 'role'
   */
  type?: 'code' | 'role';

  /**
   * 权限码判定方式：any=任一命中（并集）/ all=全部命中（交集）
   * @default 'any'
   */
  mode?: 'all' | 'any';
}

defineOptions({
  name: 'AccessControl',
});

const props = withDefaults(defineProps<Props>(), {
  codes: () => [],
  type: 'role',
  mode: 'any',
});

const { hasAccessByCodes, hasAccessByRoles, hasAllAccessByCodes } = useAccess();

const hasAuth = computed(() => {
  const { codes, mode, type } = props;
  if (type === 'role') {
    return hasAccessByRoles(codes);
  }
  return mode === 'all' ? hasAllAccessByCodes(codes) : hasAccessByCodes(codes);
});
</script>

<template>
  <slot v-if="!codes"></slot>
  <slot v-else-if="hasAuth"></slot>
</template>

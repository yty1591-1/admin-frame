<script setup lang="ts">
import type { SecuritySchemaItem } from '@vben/common-ui';

import { computed } from 'vue';

import { ProfileSecuritySetting } from '@vben/common-ui';

/**
 * 示例数据：仅演示「状态徽标 + 操作入口」的排版，不含任何真实账号信息。
 */
const formSchema = computed((): SecuritySchemaItem[] => {
  return [
    {
      fieldName: 'accountPassword',
      label: '账户密码',
      status: 'bound',
      statusLabel: '已设置',
      description: '上次修改：2026-01-01 09:00',
      actionLabel: '修改密码',
    },
    {
      fieldName: 'securityPhone',
      label: '密保手机',
      status: 'unbound',
      description: '未绑定手机，请联系管理员绑定',
    },
    {
      fieldName: 'securityQuestion',
      label: '密保问题',
      status: 'unavailable',
      description: '系统暂未开通该能力',
    },
    {
      fieldName: 'securityEmail',
      label: '备用邮箱',
      status: 'unavailable',
      description: '系统暂未开通该能力',
    },
    {
      fieldName: 'securityMfa',
      label: 'MFA 设备',
      status: 'unbound',
      description: '未绑定 MFA 设备，绑定后，可以进行二次确认',
      actionLabel: '绑定 MFA',
    },
  ];
});
</script>
<template>
  <ProfileSecuritySetting :form-schema="formSchema" />
</template>

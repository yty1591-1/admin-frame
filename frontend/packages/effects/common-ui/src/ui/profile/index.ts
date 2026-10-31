export { default as ProfileBaseSetting } from './base-setting.vue';
/**
 * 通知偏好组件仅保留导出：当前没有通知偏好后端，两端页面已不再引用。
 * 保留是为了避免 `@vben/common-ui` 对外 API 破坏性变更；后端落地前不要接入真实页面。
 */
export { default as ProfileNotificationSetting } from './notification-setting.vue';
export { default as ProfilePasswordSetting } from './password-setting.vue';
export { default as Profile } from './profile.vue';
export { default as ProfileSecuritySetting } from './security-setting.vue';
export { default as ProfileSessionList } from './session-list.vue';
export type * from './types';

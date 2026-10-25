import type { Role } from '@admin-frame/api-client';
import type { StatusTone } from '@admin-frame/ui';

export type RoleMeta = {
  readonly label: string;
  readonly tone: StatusTone;
};

/** 角色枚举与中文文案集中在 entities，页面只做查表（§3.2）。 */
export const roleChannelMeta: Record<Role['channel'], RoleMeta> = {
  admin: { label: 'A 端', tone: 'processing' },
  biz: { label: 'B 端', tone: 'processing' },
};

export const roleTypeMeta: Record<Role['role_type'], RoleMeta> = {
  system: { label: '系统角色', tone: 'processing' },
  custom: { label: '自定义', tone: 'default' },
  breakglass: { label: '破玻璃', tone: 'warning' },
};

export const roleStatusMeta: Record<Role['status'], RoleMeta> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '停用', tone: 'default' },
};

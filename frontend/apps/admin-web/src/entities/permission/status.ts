import type { Permission } from '@admin-frame/api-client';
import type { StatusTone } from '@admin-frame/ui';

export type PermissionMeta = {
  readonly label: string;
  readonly tone: StatusTone;
};

/** 业务枚举与中文文案集中在 entities，页面只做查表（§3.2）。 */
export const permissionChannelMeta: Record<Permission['channel'], PermissionMeta> = {
  admin: { label: 'A 端', tone: 'processing' },
  biz: { label: 'B 端', tone: 'processing' },
  both: { label: '双端', tone: 'default' },
};

export const permissionKindMeta: Record<Permission['kind'], PermissionMeta> = {
  api: { label: '接口', tone: 'default' },
  menu: { label: '菜单', tone: 'default' },
  button: { label: '按钮', tone: 'default' },
};

export const permissionStatusMeta: Record<Permission['status'], PermissionMeta> = {
  active: { label: '启用', tone: 'success' },
  deprecated: { label: '已废弃', tone: 'default' },
};

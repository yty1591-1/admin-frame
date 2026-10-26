import type { Permission } from '@admin-frame/api-client';
import type { MessageKey } from '@admin-frame/i18n';
import type { StatusTone } from '@admin-frame/ui';

export type PermissionMeta = {
  readonly labelKey: MessageKey;
  readonly tone: StatusTone;
};

/** 业务枚举与文案 key 集中在 entities，页面只做查表与翻译（§3.2）。 */
export const permissionChannelMeta: Record<Permission['channel'], PermissionMeta> = {
  admin: { labelKey: 'permission.catalog.channel.admin', tone: 'processing' },
  biz: { labelKey: 'permission.catalog.channel.biz', tone: 'processing' },
  both: { labelKey: 'permission.catalog.channel.both', tone: 'default' },
};

export const permissionKindMeta: Record<Permission['kind'], PermissionMeta> = {
  api: { labelKey: 'permission.catalog.kind.api', tone: 'default' },
  menu: { labelKey: 'permission.catalog.kind.menu', tone: 'default' },
  button: { labelKey: 'permission.catalog.kind.button', tone: 'default' },
};

export const permissionStatusMeta: Record<Permission['status'], PermissionMeta> = {
  active: { labelKey: 'permission.catalog.status.active', tone: 'success' },
  deprecated: { labelKey: 'permission.catalog.status.deprecated', tone: 'default' },
};

import type { Role } from '@admin-frame/api-client';
import type { MessageKey } from '@admin-frame/i18n';
import type { StatusTone } from '@admin-frame/ui';

export type RoleMeta = {
  readonly labelKey: MessageKey;
  readonly tone: StatusTone;
};

/** 角色枚举与文案 key 集中在 entities，页面只做查表与翻译（§3.2）。 */
export const roleChannelMeta: Record<Role['channel'], RoleMeta> = {
  admin: { labelKey: 'iam.role.channel.admin', tone: 'processing' },
  biz: { labelKey: 'iam.role.channel.biz', tone: 'processing' },
};

export const roleTypeMeta: Record<Role['role_type'], RoleMeta> = {
  system: { labelKey: 'iam.role.type.system', tone: 'processing' },
  custom: { labelKey: 'iam.role.type.custom', tone: 'default' },
  breakglass: { labelKey: 'iam.role.type.breakglass', tone: 'warning' },
};

export const roleStatusMeta: Record<Role['status'], RoleMeta> = {
  active: { labelKey: 'iam.role.status.active', tone: 'success' },
  disabled: { labelKey: 'iam.role.status.disabled', tone: 'default' },
};

import type { BasicUserInfo } from '@vben/types';

export interface Props {
  title?: string;
  userInfo: BasicUserInfo | null;
  tabs: {
    label: string;
    value: string;
  }[];
}

/**
 * 通知开关项：由开关直接切换的布尔配置。
 */
export interface FormSchemaItem {
  description: string;
  fieldName: string;
  label: string;
  value: boolean;
}

/**
 * 安全状态项状态。
 *
 * - `bound`：已具备（已设置密码 / 已绑定手机 / 已绑定 MFA）；
 * - `unbound`：明确未具备；
 * - `unavailable`：系统暂无该能力或状态不可获取，文案须为中性占位，禁止虚构具体值。
 */
export type SecurityItemStatus = 'bound' | 'unavailable' | 'unbound';

/**
 * 安全状态项。与开关项不同，安全能力不是「开关」，而是「状态 + 操作入口」。
 *
 * `description` 必须来自接口真实数据或显式中性占位；禁止出现虚构的手机号 / 邮箱等具体值。
 */
export interface SecuritySchemaItem {
  /** 操作按钮文案；留空表示该项无可用操作。 */
  actionLabel?: string;
  /** 操作按钮是否禁用（如接口不可用时置灰）。 */
  actionDisabled?: boolean;
  /** 状态描述，展示在标签下方。 */
  description: string;
  fieldName: string;
  label: string;
  status: SecurityItemStatus;
  /** 状态徽标文案；留空时按 `status` 取通用文案（已绑定 / 未绑定 / 暂未开通）。 */
  statusLabel?: string;
}

export interface SettingProps {
  formSchema: FormSchemaItem[];
}

export interface SecuritySettingProps {
  formSchema: SecuritySchemaItem[];
  /** 加载中显示骨架，避免闪现「未绑定」等未确认状态。 */
  loading?: boolean;
}

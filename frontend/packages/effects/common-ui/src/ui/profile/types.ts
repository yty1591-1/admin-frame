import type { Component } from 'vue';

import type { BasicUserInfo } from '@vben/types';

export interface Props {
  title?: string;
  userInfo: BasicUserInfo | null;
  /** 账号状态文案（由页面 i18n 后传入）；留空则不展示。 */
  statusLabel?: string;
  tabs: {
    /** 页签图标；可选，移动端横向页签只在有值时渲染。 */
    icon?: Component;
    label: string;
    value: string;
  }[];
}

/**
 * 只读资料项：标签 + 已格式化好的值。
 *
 * 值必须在此处完成格式化（时间、状态文案、空值兜底），共享组件不猜业务语义。
 */
export interface InfoItem {
  label: string;
  value: string;
  /** 值下方的补充说明（如「登录名由系统分配，不可修改」）；留空不渲染。 */
  description?: string;
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

/**
 * 登录设备会话项。
 *
 * 后端只返回 IP / UA 的不可逆摘要，共享组件不展示摘要，统一显示「未知设备」；
 * 时间字段传 ISO 字符串，由组件按本地时区格式化。
 */
export interface SessionItem {
  /** 设备会话 ID（后端 family_id），撤销时回传。 */
  id: string;
  /** 是否为当前设备。 */
  current?: boolean;
  status: string;
  createdAt: string;
  lastUsedAt: string;
  expiresAt: string;
  /** 设备绝对到期时间；刷新不会延长。 */
  absoluteExpiresAt?: string;
  revokedAt?: null | string;
}

/**
 * 登录设备列表的 props。
 *
 * `currentId` 用于兜底标记当前设备（接口未返回 `current` 时），避免用户误撤销自己。
 */
export interface ProfileSessionListProps {
  items: SessionItem[];
  loading?: boolean;
  /** 加载失败；为 true 时展示错误态与重试入口，不展示空态。 */
  error?: boolean;
  hasMore?: boolean;
  currentId?: string;
  /** 正在撤销的设备 ID；该行按钮进入 loading。 */
  revokingId?: string;
  /** 正在执行「撤销全部设备」。 */
  revokingAll?: boolean;
}

export interface SettingProps {
  formSchema: FormSchemaItem[];
}

export interface SecuritySettingProps {
  formSchema: SecuritySchemaItem[];
  /** 加载中显示骨架，避免闪现「未绑定」等未确认状态。 */
  loading?: boolean;
}

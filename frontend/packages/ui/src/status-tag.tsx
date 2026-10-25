import { Tag } from 'antd';

/** 语义色板：颜色只做辅助，语义必须由文本承载（§3.2）。 */
export type StatusTone = 'success' | 'processing' | 'warning' | 'error' | 'default';

const TONE_COLORS: Record<StatusTone, string | undefined> = {
  success: 'green',
  processing: 'blue',
  warning: 'orange',
  error: 'red',
  default: undefined,
};

export type StatusTagProps = {
  tone: StatusTone;
  label: string;
};

/** 业务枚举与中文文案留在 entities，ui 只负责 tone → 颜色映射。 */
export function StatusTag({ tone, label }: StatusTagProps) {
  const color = TONE_COLORS[tone];
  return color === undefined ? <Tag>{label}</Tag> : <Tag color={color}>{label}</Tag>;
}

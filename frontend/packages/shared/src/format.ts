import dayjs from 'dayjs';
import timezone from 'dayjs/plugin/timezone';
import utc from 'dayjs/plugin/utc';

dayjs.extend(utc);
dayjs.extend(timezone);

export const DEFAULT_TIME_ZONE = 'Asia/Shanghai';

/** 传输/存储统一 RFC3339（UTC），展示按目标时区格式化。 */
export function formatDateTime(value: string, timeZone: string = DEFAULT_TIME_ZONE): string {
  const parsed = dayjs(value);
  if (!parsed.isValid()) {
    return '-';
  }
  return parsed.tz(timeZone).format('YYYY-MM-DD HH:mm:ss');
}

const amountFormatters = new Map<string, Intl.NumberFormat>();

/** 金额统一格式化，禁止页面内自行 toFixed 拼接。 */
export function formatAmount(value: number, currency = 'CNY'): string {
  if (!Number.isFinite(value)) {
    return '-';
  }
  let formatter = amountFormatters.get(currency);
  if (!formatter) {
    formatter = new Intl.NumberFormat('zh-CN', {
      style: 'currency',
      currency,
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });
    amountFormatters.set(currency, formatter);
  }
  return formatter.format(value);
}

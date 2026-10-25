import { clampPageSize } from './pagination';

export type QueryParamValue = string | number | boolean | null | undefined;

export type NormalizedQueryParams = Record<string, string | number | boolean>;

/**
 * 去除空值并按 key 排序，保证 Query Key 与 URL 序列化稳定（§8.2）。
 * 不做类型转换：调用方负责传入已规范化的值。
 */
export function normalizeQueryParams(
  params: Readonly<Record<string, QueryParamValue>>,
): NormalizedQueryParams {
  const entries = Object.entries(params)
    .filter((entry): entry is [string, string | number | boolean] => {
      const value = entry[1];
      return value !== undefined && value !== null;
    })
    .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0));
  return Object.fromEntries(entries);
}

/** URL 页码：非法或缺省回落 1。 */
export function parsePage(raw: string | null): number {
  const parsed = Number.parseInt(raw ?? '', 10);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

/** URL 页大小：非法回落默认值，超过上限按 100 收敛（§9.4）。 */
export function parsePageSize(raw: string | null): number {
  const parsed = Number.parseInt(raw ?? '', 10);
  return clampPageSize(Number.isInteger(parsed) ? parsed : Number.NaN);
}

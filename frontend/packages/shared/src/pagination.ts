/** 分页默认值与上限，与 API_STANDARDS §4 / 前端架构 §9.4 一致。 */
export const defaultPageSize = 20;

export const maxPageSize = 100;

/** 前端控件只允许 1～100 的整数页大小；非法值回落默认值。 */
export function clampPageSize(value: number): number {
  if (!Number.isInteger(value) || value < 1) {
    return defaultPageSize;
  }
  return Math.min(value, maxPageSize);
}

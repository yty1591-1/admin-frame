/**
 * 判断循环依赖数量是否超过阈值。
 * 检查脚本必须以此判定退出码，否则门禁恒为绿。
 * @param count - 检测到的循环依赖数量
 * @param threshold - 允许的阈值
 * @returns 需要判为失败时为 true
 */
function exceedsThreshold(count: number, threshold: number): boolean {
  return count > threshold;
}

export { exceedsThreshold };

/**
 * 写操作幂等键（§7.3）：UUIDv4。
 * 同一次提交的重试必须复用同一个 key，调用方应在提交开始时生成并保存。
 */
export function createIdempotencyKey(): string {
  return globalThis.crypto.randomUUID();
}

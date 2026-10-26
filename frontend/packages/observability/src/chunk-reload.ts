/**
 * chunk 加载失败识别与「仅刷新一次」去重（S3 / §8.3）。
 * 注意：本文件是唯一允许访问 sessionStorage 的例外，仅存一次性刷新标记，
 * 键名与值均不含令牌或用户数据（FE-07）。
 */
export const CHUNK_RELOAD_STORAGE_KEY = 'admin_frame_chunk_reload_guard';

export type ChunkReloadStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

const MAX_CAUSE_DEPTH = 3;

const CHUNK_MESSAGE_PATTERNS: readonly RegExp[] = [
  /failed to fetch dynamically imported module/i,
  /error loading dynamically imported module/i,
  /loading chunk [\w-]+ failed/i,
  /importing a module script failed/i,
];

function defaultStorage(): ChunkReloadStorage | null {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

/** 识别 webpack/Vite 的动态 chunk 加载失败（含 cause 链）。 */
export function isChunkLoadError(error: unknown): boolean {
  let current: unknown = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH; depth += 1) {
    if (typeof current !== 'object' || current === null) {
      return false;
    }
    const record = current as Record<string, unknown>;
    let name: unknown;
    let message: unknown;
    let cause: unknown;
    try {
      name = record['name'];
      message = record['message'];
      cause = record['cause'];
    } catch {
      return false;
    }
    if (name === 'ChunkLoadError') {
      return true;
    }
    if (typeof message === 'string' && CHUNK_MESSAGE_PATTERNS.some((p) => p.test(message))) {
      return true;
    }
    if (cause === undefined || cause === null || cause === current) {
      return false;
    }
    current = cause;
  }
  return false;
}

/**
 * 本次会话是否允许为 chunk 失败刷新一次。
 * 首次返回 true（调用方执行 location.reload），第二次返回 false（仅提示手动刷新）。
 */
export function reloadOnceForChunkError(
  storage: ChunkReloadStorage | null = defaultStorage(),
): boolean {
  if (storage === null) {
    return false;
  }
  try {
    if (storage.getItem(CHUNK_RELOAD_STORAGE_KEY) === '1') {
      return false;
    }
    storage.setItem(CHUNK_RELOAD_STORAGE_KEY, '1');
    return true;
  } catch {
    return false;
  }
}

/** 重置标记（例如发布后手动恢复，或测试隔离）。 */
export function resetChunkReloadGuard(storage: ChunkReloadStorage | null = defaultStorage()): void {
  if (storage === null) {
    return;
  }
  try {
    storage.removeItem(CHUNK_RELOAD_STORAGE_KEY);
  } catch {
    // 存储不可用时无需处理。
  }
}

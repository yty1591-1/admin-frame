import { describe, expect, it } from 'vitest';
import {
  CHUNK_RELOAD_STORAGE_KEY,
  isChunkLoadError,
  reloadOnceForChunkError,
  resetChunkReloadGuard,
  type ChunkReloadStorage,
} from './chunk-reload';

function createStorage(): ChunkReloadStorage {
  const data = new Map<string, string>();
  return {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      data.set(key, value);
    },
    removeItem: (key) => {
      data.delete(key);
    },
  };
}

describe('isChunkLoadError', () => {
  it('识别 ChunkLoadError 与常见动态导入失败文案', () => {
    expect(isChunkLoadError(Object.assign(new Error('x'), { name: 'ChunkLoadError' }))).toBe(true);
    expect(
      isChunkLoadError(new Error('Failed to fetch dynamically imported module: /assets/a.js')),
    ).toBe(true);
    expect(isChunkLoadError(new Error('error loading dynamically imported module'))).toBe(true);
    expect(isChunkLoadError(new Error('Loading chunk 42 failed'))).toBe(true);
    expect(isChunkLoadError(new Error('Importing a module script failed'))).toBe(true);
  });

  it('沿 cause 链识别', () => {
    const wrapper = new Error('wrapper', { cause: new Error('Loading chunk 7 failed') });

    expect(isChunkLoadError(wrapper)).toBe(true);
  });

  it('非 chunk 错误与原始值恒为 false', () => {
    expect(isChunkLoadError(new Error('network down'))).toBe(false);
    expect(isChunkLoadError(null)).toBe(false);
    expect(isChunkLoadError('Loading chunk 1 failed')).toBe(false);
  });
});

describe('reloadOnceForChunkError', () => {
  it('同一会话仅首次返回 true', () => {
    const storage = createStorage();

    expect(reloadOnceForChunkError(storage)).toBe(true);
    expect(storage.getItem(CHUNK_RELOAD_STORAGE_KEY)).toBe('1');
    expect(reloadOnceForChunkError(storage)).toBe(false);
  });

  it('存储不可用或读写异常时返回 false', () => {
    expect(reloadOnceForChunkError(null)).toBe(false);

    const broken: ChunkReloadStorage = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => undefined,
      removeItem: () => undefined,
    };
    expect(reloadOnceForChunkError(broken)).toBe(false);
  });

  it('reset 后允许再次刷新一次', () => {
    const storage = createStorage();
    expect(reloadOnceForChunkError(storage)).toBe(true);

    resetChunkReloadGuard(storage);

    expect(storage.getItem(CHUNK_RELOAD_STORAGE_KEY)).toBeNull();
    expect(reloadOnceForChunkError(storage)).toBe(true);
  });
});

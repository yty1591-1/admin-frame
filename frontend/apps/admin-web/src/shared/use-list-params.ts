import { clampPageSize, defaultPageSize, parsePage, parsePageSize } from '@admin-frame/shared';
import { useCallback } from 'react';
import { useSearchParams } from 'react-router';

export type ListParams<TFilterKey extends string> = {
  page: number;
  pageSize: number;
  filters: Readonly<Partial<Record<TFilterKey, string>>>;
  setPage: (page: number) => void;
  setPageSize: (pageSize: number) => void;
  setFilter: (key: TFilterKey, value: string | null) => void;
  resetFilters: () => void;
};

/**
 * 列表 URL 状态（§8.1）：分页/筛选写入 URL，切换筛选重置页码，页大小收敛到 1～100。
 * `filterKeys` 必须是模块级常量，避免每次渲染产生新引用。
 */
export function useListParams<TFilterKey extends string = never>(
  filterKeys: readonly TFilterKey[] = [],
): ListParams<TFilterKey> {
  const [searchParams, setSearchParams] = useSearchParams();
  const page = parsePage(searchParams.get('page'));
  const pageSize = parsePageSize(searchParams.get('page_size'));

  const update = useCallback(
    (mutate: (next: URLSearchParams) => void) => {
      setSearchParams((previous) => {
        const next = new URLSearchParams(previous);
        mutate(next);
        return next;
      });
    },
    [setSearchParams],
  );

  const setPage = useCallback(
    (value: number) => {
      update((next) => {
        if (value <= 1) {
          next.delete('page');
        } else {
          next.set('page', String(value));
        }
      });
    },
    [update],
  );

  const setPageSize = useCallback(
    (value: number) => {
      update((next) => {
        const size = clampPageSize(value);
        if (size === defaultPageSize) {
          next.delete('page_size');
        } else {
          next.set('page_size', String(size));
        }
        next.delete('page');
      });
    },
    [update],
  );

  const setFilter = useCallback(
    (key: TFilterKey, value: string | null) => {
      update((next) => {
        const normalized = value?.trim() ?? '';
        if (normalized === '') {
          next.delete(key);
        } else {
          next.set(key, normalized);
        }
        // 筛选变化必须回到第一页，避免落在已不存在的页码（§9.4）。
        next.delete('page');
      });
    },
    [update],
  );

  const resetFilters = useCallback(() => {
    update((next) => {
      for (const key of filterKeys) {
        next.delete(key);
      }
      next.delete('page');
    });
  }, [filterKeys, update]);

  const filters: Partial<Record<TFilterKey, string>> = {};
  for (const key of filterKeys) {
    const value = searchParams.get(key);
    if (value) {
      filters[key] = value;
    }
  }

  return { page, pageSize, filters, setPage, setPageSize, setFilter, resetFilters };
}

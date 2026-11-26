import { describe, expect, it, vi } from 'vitest';

import {
  authenticateResponseInterceptor,
  defaultResponseInterceptor,
  errorMessageResponseInterceptor,
} from './preset-interceptors';

describe('errorMessageResponseInterceptor', () => {
  it('reports the status-specific message by default', async () => {
    const makeErrorMessage = vi.fn();
    const interceptor = errorMessageResponseInterceptor(makeErrorMessage);
    const error = { config: {}, response: { status: 404 } };

    await expect(interceptor.rejected?.(error)).rejects.toBe(error);

    expect(makeErrorMessage).toHaveBeenCalledTimes(1);
  });

  it('skips the message when the request sets skipErrorMessage', async () => {
    const makeErrorMessage = vi.fn();
    const interceptor = errorMessageResponseInterceptor(makeErrorMessage);
    const error = {
      config: { skipErrorMessage: true },
      response: { status: 404 },
    };

    await expect(interceptor.rejected?.(error)).rejects.toBe(error);

    expect(makeErrorMessage).not.toHaveBeenCalled();
  });
});

describe('defaultResponseInterceptor', () => {
  const interceptor = defaultResponseInterceptor({
    codeField: 'code',
    dataField: 'data',
    successCode: 0,
  });

  it('unwraps data on success', () => {
    const result = interceptor.fulfilled?.({
      config: {},
      data: { code: 0, data: { id: 1 } },
      status: 200,
    } as any);
    expect(result).toEqual({ id: 1 });
  });

  it('treats 204 No Content as success with empty payload', () => {
    const result = interceptor.fulfilled?.({
      config: {},
      data: undefined,
      status: 204,
    } as any);
    expect(result).toBeUndefined();
  });

  it('rejects non-zero business codes', () => {
    interface Thrown {
      response?: { data?: { code?: number } };
    }
    const attempt = (): { error?: Thrown; thrown: boolean } => {
      try {
        interceptor.fulfilled?.({
          config: {},
          data: { code: 50_000, message: 'boom' },
          status: 200,
        } as any);
        return { thrown: false };
      } catch (error) {
        return { error: error as Thrown, thrown: true };
      }
    };
    const outcome = attempt();
    expect(outcome.thrown).toBe(true);
    expect(outcome.error?.response?.data?.code).toBe(50_000);
  });
});

describe('authenticateResponseInterceptor', () => {
  function createClient() {
    return {
      isRefreshing: false,
      refreshTokenQueue: [] as ((token: string) => void)[],
      request: vi.fn(),
    };
  }

  function createInterceptor(
    overrides: Partial<{
      doRefreshToken: () => Promise<string>;
      doReAuthenticate: () => Promise<void>;
      enableRefreshToken: boolean;
    }> = {},
  ) {
    const client = createClient();
    const doReAuthenticate = vi.fn(
      overrides.doReAuthenticate ?? (async () => {}),
    );
    const doRefreshToken = vi.fn(
      overrides.doRefreshToken ?? (async () => 'new-token'),
    );
    const interceptor = authenticateResponseInterceptor({
      client: client as any,
      doReAuthenticate,
      doRefreshToken,
      enableRefreshToken: overrides.enableRefreshToken ?? true,
      formatToken: (token) => (token ? `Bearer ${token}` : null),
    });
    return { client, doReAuthenticate, doRefreshToken, interceptor };
  }

  it('keeps非 401 错误原样抛出', async () => {
    const { interceptor, doReAuthenticate } = createInterceptor();
    const error = { config: {}, response: { status: 500 } };

    await expect(interceptor.rejected?.(error)).rejects.toBe(error);
    expect(doReAuthenticate).not.toHaveBeenCalled();
  });

  it('401 且未启用刷新时直接重认证', async () => {
    const { interceptor, doReAuthenticate } = createInterceptor({
      enableRefreshToken: false,
    });
    const error = { config: {}, response: { status: 401 } };

    await expect(interceptor.rejected?.(error)).rejects.toBe(error);
    expect(doReAuthenticate).toHaveBeenCalledTimes(1);
  });

  it('401 刷新成功后带新令牌重试请求', async () => {
    const { interceptor, client, doRefreshToken } = createInterceptor();
    client.request.mockResolvedValueOnce('retried');
    const config: any = { headers: {}, url: '/api/me' };
    const error = { config, response: { status: 401 } };

    await expect(interceptor.rejected?.(error)).resolves.toBe('retried');

    expect(doRefreshToken).toHaveBeenCalledTimes(1);
    expect(config.headers.Authorization).toBe('Bearer new-token');
    expect(config.__isRetryRequest).toBe(true);
    expect(client.request).toHaveBeenCalledWith('/api/me', config);
  });

  it('刷新失败时重认证并拒绝，队列收到空令牌', async () => {
    const { interceptor, client, doReAuthenticate } = createInterceptor({
      doRefreshToken: async () => {
        throw new Error('refresh failed');
      },
    });
    const queued = vi.fn();
    client.refreshTokenQueue.push(queued);
    const error = { config: {}, response: { status: 401 } };

    await expect(interceptor.rejected?.(error)).rejects.toThrow(
      'refresh failed',
    );
    expect(doReAuthenticate).toHaveBeenCalledTimes(1);
    expect(queued).toHaveBeenCalledWith('');
  });

  it('并发 401 排队等待刷新结果（单飞）', async () => {
    const { interceptor, client } = createInterceptor();
    client.isRefreshing = true;
    client.request.mockResolvedValueOnce('queued-result');
    const error = { config: { headers: {} }, response: { status: 401 } };

    const pending = interceptor.rejected?.(error) as Promise<unknown>;
    expect(client.refreshTokenQueue).toHaveLength(1);

    const [notify] = client.refreshTokenQueue;
    if (!notify) {
      throw new Error('refresh queue should contain the waiting request');
    }
    notify('queued-token');
    await expect(pending).resolves.toBe('queued-result');
    expect(client.request).toHaveBeenCalledWith(undefined, {
      headers: { Authorization: 'Bearer queued-token' },
    });
  });
});

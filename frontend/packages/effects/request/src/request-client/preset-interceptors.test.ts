import { describe, expect, it, vi } from 'vitest';

import { errorMessageResponseInterceptor } from './preset-interceptors';

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

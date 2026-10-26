import { defaultTranslator } from '@admin-frame/i18n';
import { renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppErrorBoundary } from './AppErrorBoundary';

const t = defaultTranslator;

function Bomb(): never {
  throw new Error('boom');
}

beforeEach(() => {
  // React 会把边界捕获的异常打印到 console.error，测试内静音避免噪声。
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AppErrorBoundary', () => {
  it('渲染致命错误页并提供 trace_id 与重试入口', async () => {
    renderWithProviders(
      <AppErrorBoundary>
        <Bomb />
      </AppErrorBoundary>,
      { toast: false },
    );

    expect(await screen.findByText(t('common.pageLoadFailed'))).toBeInTheDocument();
    expect(screen.getByText(t('common.pageLoadFailedDescription'))).toBeInTheDocument();
    // 原始异常 message 不得透出给用户（§9.3）。
    expect(screen.queryByText(/boom/)).not.toBeInTheDocument();
    const alert = screen.getByRole('alert');
    expect(alert.textContent).toMatch(/trace/i);
    // Ant Design 会在两个汉字之间插入空格（“重 试”），用宽松匹配。
    const retryPattern = new RegExp(t('common.retry').split('').join('\\s*'));
    expect(screen.getByRole('button', { name: retryPattern })).toBeInTheDocument();
  });

  it('无异常时透传子节点', () => {
    renderWithProviders(
      <AppErrorBoundary>
        <span>ok</span>
      </AppErrorBoundary>,
      { toast: false },
    );

    expect(screen.getByText('ok')).toBeInTheDocument();
  });
});

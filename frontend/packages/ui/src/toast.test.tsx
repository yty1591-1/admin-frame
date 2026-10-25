import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { ToastProvider, useToast } from './toast';

function ToastProbe() {
  const toast = useToast();
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          toast.success('保存成功');
        }}
      >
        成功
      </button>
      <button
        type="button"
        onClick={() => {
          toast.error({ message: '保存失败', traceId: 'trace-7' });
        }}
      >
        失败
      </button>
    </div>
  );
}

describe('ToastProvider', () => {
  it('成功通知通过统一出口展示', async () => {
    render(
      <ToastProvider>
        <ToastProbe />
      </ToastProvider>,
    );

    await userEvent.click(screen.getByRole('button', { name: '成功' }));
    expect(await screen.findByText('保存成功')).toBeInTheDocument();
  });

  it('失败通知展示可复制的 trace_id', async () => {
    render(
      <ToastProvider>
        <ToastProbe />
      </ToastProvider>,
    );

    await userEvent.click(screen.getByRole('button', { name: '失败' }));
    expect(await screen.findByText('保存失败')).toBeInTheDocument();
    expect(await screen.findByText(/trace-7/)).toBeInTheDocument();
  });
});

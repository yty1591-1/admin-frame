import '@testing-library/jest-dom/vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DangerActionConfirm, type DangerActionProps } from './danger-action';

function renderDanger(props: Partial<DangerActionProps> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  render(
    <DangerActionConfirm
      open
      title="删除角色"
      target="运营主管"
      impacts={['解除全部绑定的权限', '历史审计保留']}
      onConfirm={onConfirm}
      onCancel={onCancel}
      {...props}
    />,
  );
  return { onConfirm, onCancel };
}

describe('DangerActionConfirm', () => {
  it('展示影响范围，原因必填时确认按钮禁用', async () => {
    const { onConfirm } = renderDanger();

    expect(screen.getByText('解除全部绑定的权限')).toBeInTheDocument();
    expect(screen.getByText('历史审计保留')).toBeInTheDocument();

    const confirm = screen.getByRole('button', { name: '删除角色' });
    expect(confirm).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/操作原因/), '   ');
    expect(confirm).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/操作原因/), '离职交接');
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith('离职交接');
  });

  it('错误态展示 trace_id', () => {
    renderDanger({
      error: { message: '删除角色失败，请稍后重试。', traceId: 'trace-40900' },
    });

    expect(screen.getByText('删除角色失败，请稍后重试。')).toBeInTheDocument();
    expect(screen.getByText(/trace-40900/)).toBeInTheDocument();
  });

  it('提交中按 Esc 不关闭弹窗', () => {
    const { onCancel } = renderDanger({ loading: true });

    const wrapper = document.querySelector('.ant-modal-wrap');
    if (wrapper) {
      fireEvent.keyDown(wrapper, { key: 'Escape', keyCode: 27 });
    }
    expect(onCancel).not.toHaveBeenCalled();
  });
});

import '@testing-library/jest-dom/vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog, type ConfirmDialogProps } from './dialog';

function renderDialog(props: Partial<ConfirmDialogProps> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  render(
    <ConfirmDialog
      open
      title="删除角色"
      content={<p>删除后不可恢复</p>}
      onConfirm={onConfirm}
      onCancel={onCancel}
      {...props}
    />,
  );
  return { onConfirm, onCancel };
}

describe('ConfirmDialog', () => {
  it('确认与取消分别触发对应回调', async () => {
    const { onConfirm, onCancel } = renderDialog();

    expect(screen.getByText('删除后不可恢复')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /确\s*定/ }));
    expect(onConfirm).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole('button', { name: /取\s*消/ }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('按 Esc 关闭弹窗并回调 onCancel', () => {
    const { onCancel } = renderDialog();

    // antd 的 Esc 监听挂载在弹窗容器上，需在容器内触发键事件。
    const wrapper = document.querySelector('.ant-modal-wrap');
    expect(wrapper).not.toBeNull();
    if (wrapper) {
      fireEvent.keyDown(wrapper, { key: 'Escape', keyCode: 27 });
    }
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('提交中时确认按钮不可重复触发', async () => {
    const { onConfirm } = renderDialog({ loading: true });

    await userEvent.click(screen.getByRole('button', { name: /确\s*定/ }));
    expect(onConfirm).not.toHaveBeenCalled();
  });
});

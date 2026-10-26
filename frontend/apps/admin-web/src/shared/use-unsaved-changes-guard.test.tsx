import { createTestRouter, renderWithProviders } from '@admin-frame/testing';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RouterProvider, useNavigate } from 'react-router';
import { describe, expect, it } from 'vitest';
import { useUnsavedChangesGuard } from './use-unsaved-changes-guard';

function GuardedForm({ when }: { when: boolean }) {
  const guard = useUnsavedChangesGuard(when);
  const navigate = useNavigate();

  return (
    <div>
      <button
        onClick={() => {
          void navigate('/next');
        }}
      >
        leave
      </button>
      <div data-testid="blocked">{guard.blocked ? 'yes' : 'no'}</div>
      {guard.blocked ? (
        <>
          <button onClick={guard.proceed}>proceed</button>
          <button onClick={guard.reset}>reset</button>
        </>
      ) : null}
    </div>
  );
}

function renderGuard(when: boolean) {
  const router = createTestRouter(
    [
      { path: '/form', element: <GuardedForm when={when} /> },
      { path: '/next', element: <div>next-page</div> },
    ],
    ['/form'],
  );
  return renderWithProviders(<RouterProvider router={router} />, { toast: false });
}

describe('useUnsavedChangesGuard', () => {
  it('站内跳转被拦截，确认后放行', async () => {
    const user = userEvent.setup();
    renderGuard(true);

    await user.click(screen.getByRole('button', { name: 'leave' }));

    await waitFor(() => {
      expect(screen.getByTestId('blocked')).toHaveTextContent('yes');
    });
    expect(screen.queryByText('next-page')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'proceed' }));

    expect(await screen.findByText('next-page')).toBeInTheDocument();
  });

  it('取消离开后留在当前页并解除拦截', async () => {
    const user = userEvent.setup();
    renderGuard(true);

    await user.click(screen.getByRole('button', { name: 'leave' }));
    await waitFor(() => {
      expect(screen.getByTestId('blocked')).toHaveTextContent('yes');
    });

    await user.click(screen.getByRole('button', { name: 'reset' }));

    await waitFor(() => {
      expect(screen.getByTestId('blocked')).toHaveTextContent('no');
    });
    expect(screen.queryByText('next-page')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'leave' })).toBeInTheDocument();
  });

  it('无未保存修改时直接跳转', async () => {
    const user = userEvent.setup();
    renderGuard(false);

    await user.click(screen.getByRole('button', { name: 'leave' }));

    expect(await screen.findByText('next-page')).toBeInTheDocument();
  });

  it('仅在有未保存修改时阻止浏览器关闭/刷新', () => {
    renderGuard(true);

    const blockedEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(blockedEvent);
    expect(blockedEvent.defaultPrevented).toBe(true);
  });

  it('无修改时不注册 beforeunload 阻止', () => {
    renderGuard(false);

    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
  });
});

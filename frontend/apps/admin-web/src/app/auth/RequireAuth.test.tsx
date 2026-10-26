import { defaultTranslator } from '@admin-frame/i18n';
import { renderWithProviders } from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router';
import { describe, expect, it } from 'vitest';
import { AuthHarness } from '../../testing/auth-harness';
import { RequireAuth } from './RequireAuth';

const t = defaultTranslator;

function LoginProbe() {
  const location = useLocation();
  return <div>{`login-marker:${location.search}`}</div>;
}

describe('RequireAuth', () => {
  it('启动期恢复会话时保持加载态，不提前跳转', () => {
    renderWithProviders(
      <AuthHarness initialStatus="booting">
        <Routes>
          <Route path="/login" element={<LoginProbe />} />
          <Route
            path="/iam/roles"
            element={
              <RequireAuth>
                <div>protected-content</div>
              </RequireAuth>
            }
          />
        </Routes>
      </AuthHarness>,
      { initialEntries: ['/iam/roles'], toast: false },
    );

    expect(screen.getByRole('status')).toHaveTextContent(t('common.loading'));
    expect(screen.queryByText('login-marker:')).not.toBeInTheDocument();
  });

  it('未登录时跳转登录页并携带 returnTo', async () => {
    renderWithProviders(
      <AuthHarness initialStatus="anonymous">
        <Routes>
          <Route path="/login" element={<LoginProbe />} />
          <Route
            path="/iam/roles"
            element={
              <RequireAuth>
                <div>protected-content</div>
              </RequireAuth>
            }
          />
        </Routes>
      </AuthHarness>,
      { initialEntries: ['/iam/roles?page=2'], toast: false },
    );

    expect(await screen.findByText(/login-marker:/)).toBeInTheDocument();
    // returnTo 只允许站内相对路径，并完整保留查询参数（§17.4）。
    expect(
      screen.getByText(`login-marker:?returnTo=${encodeURIComponent('/iam/roles?page=2')}`),
    ).toBeInTheDocument();
    expect(screen.queryByText('protected-content')).not.toBeInTheDocument();
  });

  it('已登录时渲染受保护内容', () => {
    renderWithProviders(
      <AuthHarness initialStatus="authenticated">
        <Routes>
          <Route path="/login" element={<LoginProbe />} />
          <Route
            path="/iam/roles"
            element={
              <RequireAuth>
                <div>protected-content</div>
              </RequireAuth>
            }
          />
        </Routes>
      </AuthHarness>,
      { initialEntries: ['/iam/roles'], toast: false },
    );

    expect(screen.getByText('protected-content')).toBeInTheDocument();
  });
});

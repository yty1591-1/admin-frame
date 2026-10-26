import { defaultTranslator } from '@admin-frame/i18n';
import {
  createAdminHandlers,
  createTestServer,
  findSeriousA11yViolations,
  renderWithProviders,
} from '@admin-frame/testing';
import { screen } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { Route, Routes } from 'react-router';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { AuthHarness } from '../testing/auth-harness';
import LoginPage from './LoginPage';

const t = defaultTranslator;
const server = createTestServer(createAdminHandlers());

/** antd 会在两个汉字之间插入空格，标签/按钮可访问名匹配需要放宽。 */
function looseName(text: string): RegExp {
  return new RegExp([...text].join('\\s*'));
}

function labelOf(text: string): HTMLElement {
  return screen.getByLabelText(looseName(text));
}

function buttonOf(text: string): HTMLElement {
  return screen.getByRole('button', { name: looseName(text) });
}

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' });
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

function renderLogin() {
  return renderWithProviders(
    <AuthHarness>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<div>home-marker</div>} />
      </Routes>
    </AuthHarness>,
    { initialEntries: ['/login'] },
  );
}

async function submitCredentials(user: UserEvent): Promise<void> {
  await user.type(labelOf(t('auth.subject')), 'staff-1');
  await user.type(labelOf(t('auth.password')), 'secret');
  await user.click(buttonOf(t('auth.login')));
}

describe('LoginPage', () => {
  it('渲染登录表单且无严重可访问性问题', async () => {
    const { container } = renderLogin();

    expect(await screen.findByText(t('auth.loginIntro'))).toBeInTheDocument();
    expect(labelOf(t('auth.subject'))).toBeInTheDocument();
    expect(labelOf(t('auth.password'))).toBeInTheDocument();
    expect(await findSeriousA11yViolations(container)).toEqual([]);
  });

  it('未填写凭据时展示校验提示', async () => {
    const user = userEvent.setup();
    renderLogin();
    await screen.findByText(t('auth.loginIntro'));

    await user.click(buttonOf(t('auth.login')));

    expect(await screen.findByText(t('auth.credentialsRequired'))).toBeInTheDocument();
    expect(await screen.findByText(t('auth.subjectRequired'))).toBeInTheDocument();
    expect(await screen.findByText(t('auth.passwordRequired'))).toBeInTheDocument();
  });

  it('40100 展示统一文案与 trace_id', async () => {
    server.use(...createAdminHandlers({ loginOutcome: 'unauthorized' }));
    const user = userEvent.setup();
    renderLogin();
    await screen.findByText(t('auth.loginIntro'));

    await submitCredentials(user);

    expect(await screen.findByText(t('auth.invalidCredentials'))).toBeInTheDocument();
    expect(screen.getByText(t('common.traceId', { traceId: 'test-trace' }))).toBeInTheDocument();
    // 40100 不区分账号不存在与密码错误，且不得回显口令（§10.6）。
    expect(screen.queryByText('secret')).not.toBeInTheDocument();
  });

  it('需要 MFA 时进入两步验证并登录成功', async () => {
    server.use(...createAdminHandlers({ loginOutcome: 'mfa' }));
    const user = userEvent.setup();
    renderLogin();
    await screen.findByText(t('auth.loginIntro'));

    await submitCredentials(user);

    expect(await screen.findByText(t('auth.mfaPrompt'))).toBeInTheDocument();
    await user.type(labelOf(t('auth.mfaCode')), '123456');
    await user.click(buttonOf(t('auth.verify')));

    expect(await screen.findByText('home-marker')).toBeInTheDocument();
  });

  it('动态码错误时回到登录页并提示', async () => {
    server.use(...createAdminHandlers({ loginOutcome: 'mfa', mfaVerifyOutcome: 'invalid' }));
    const user = userEvent.setup();
    renderLogin();
    await screen.findByText(t('auth.loginIntro'));

    await submitCredentials(user);
    await screen.findByText(t('auth.mfaPrompt'));
    await user.type(labelOf(t('auth.mfaCode')), '000000');
    await user.click(buttonOf(t('auth.verify')));

    expect(await screen.findByText(t('auth.mfaInvalid'))).toBeInTheDocument();
    expect(await screen.findByText(t('auth.loginIntro'))).toBeInTheDocument();
  });

  it('登录前需绑定 MFA 时展示密钥与恢复码', async () => {
    server.use(...createAdminHandlers({ loginOutcome: 'mfaSetup' }));
    const user = userEvent.setup();
    renderLogin();
    await screen.findByText(t('auth.loginIntro'));

    await submitCredentials(user);

    expect(await screen.findByText(t('auth.mfaSetupRequired'))).toBeInTheDocument();
    expect(screen.getByText('JBSWY3DPEHPK3PXP')).toBeInTheDocument();
    expect(screen.getByText('recovery-1')).toBeInTheDocument();
    expect(screen.queryByText('home-marker')).not.toBeInTheDocument();

    await user.click(buttonOf(t('auth.mfaRecoverySaved')));
    await user.type(labelOf(t('auth.mfaCode')), '123456');
    await user.click(buttonOf(t('auth.verify')));

    expect(await screen.findByText('home-marker')).toBeInTheDocument();
  });
});

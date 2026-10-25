import { isAppError } from '@admin-frame/api-client';
import type { MFASetupData } from '@admin-frame/api-client';
import {
  Alert,
  Button,
  Card,
  Divider,
  Input,
  PageLoading,
  Space,
  Typography,
} from '@admin-frame/ui';
import { useState, type CSSProperties, type FormEvent } from 'react';
import { useForm } from 'react-hook-form';
import { Navigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { useAuth } from '../app/auth/auth-context';

const loginSchema = z.object({
  subject: z.string().trim().min(1),
  password: z.string().min(1),
});

type LoginValues = z.infer<typeof loginSchema>;

type LoginError = {
  message: string;
  traceId: string | undefined;
};

/** credentials：密码步骤；mfa：两步登录；mfaSetup：登录前强制绑定（§7.2.1）。 */
type Step = 'credentials' | 'mfa' | 'mfaSetup';

const RESERVED_PREFIXES = new Set(['admin', 'biz']);

/** 只接受站内相对路径，避免 returnTo 跳出本端或落入 API 前缀（§17.4）。 */
function resolveReturnTo(raw: string | null): string {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//')) {
    return '/';
  }
  const firstSegment = raw.split('/').filter(Boolean)[0] ?? '';
  return RESERVED_PREFIXES.has(firstSegment) ? '/' : raw;
}

/** 40100 统一文案，避免区分账号不存在/密码错误/停用（§10.6）。 */
function toLoginError(cause: unknown, unauthorizedMessage: string): LoginError {
  if (isAppError(cause)) {
    return {
      message: cause.code === 40100 ? unauthorizedMessage : cause.message,
      traceId: cause.traceId,
    };
  }
  return { message: '登录失败，请稍后重试', traceId: undefined };
}

const pageStyle: CSSProperties = {
  minHeight: '100vh',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: '#f5f5f5',
};

const fieldStyle: CSSProperties = { display: 'block', marginBottom: 4 };

const recoveryListStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
  gap: '4px 12px',
  margin: '8px 0 0',
  padding: 0,
  listStyle: 'none',
  fontFamily: 'monospace',
};

function ErrorAlert({ error }: { error: LoginError | null }) {
  if (!error) {
    return null;
  }
  return (
    <Alert
      type="error"
      showIcon
      style={{ marginBottom: 16 }}
      message={error.message}
      description={error.traceId ? `追踪号：${error.traceId}` : undefined}
    />
  );
}

/** 强制绑定第一步：展示密钥、otpauth URI 与一次性恢复码。 */
function MfaSetupPanel({ setup }: { setup: MFASetupData }) {
  return (
    <>
      <Typography.Paragraph type="secondary">
        首次登录需要绑定身份验证器。请将下方密钥（或 otpauth 链接）录入验证器后，再进行校验。
      </Typography.Paragraph>
      <Space direction="vertical" size={4} style={{ width: '100%', marginBottom: 12 }}>
        <Typography.Text strong>密钥</Typography.Text>
        <Typography.Text code copyable>
          {setup.secret}
        </Typography.Text>
        <Typography.Text strong>otpauth 链接</Typography.Text>
        <Typography.Text code copyable ellipsis>
          {setup.otpauth_uri}
        </Typography.Text>
      </Space>
      <Divider style={{ margin: '12px 0' }} />
      <Typography.Paragraph type="warning" style={{ marginBottom: 0 }}>
        恢复码只能查看一次，请立即保存；每个恢复码仅可使用一次。
      </Typography.Paragraph>
      <ul style={recoveryListStyle}>
        {setup.recovery_codes.map((code) => (
          <li key={code}>
            <Typography.Text code>{code}</Typography.Text>
          </li>
        ))}
      </ul>
    </>
  );
}

export default function LoginPage() {
  const { status, runtime, markAuthenticated } = useAuth();
  const [searchParams] = useSearchParams();
  const returnTo = resolveReturnTo(searchParams.get('returnTo'));
  const [step, setStep] = useState<Step>('credentials');
  const [setup, setSetup] = useState<MFASetupData | null>(null);
  const [recoverySaved, setRecoverySaved] = useState(false);
  const [error, setError] = useState<LoginError | null>(null);
  const [code, setCode] = useState('');
  const [verifying, setVerifying] = useState(false);
  const { register, handleSubmit, formState } = useForm<LoginValues>({
    defaultValues: { subject: '', password: '' },
  });

  if (status === 'booting') {
    return <PageLoading tip="正在恢复会话…" />;
  }
  if (status === 'authenticated') {
    return <Navigate to={returnTo} replace />;
  }

  /** 回到密码步骤：两种票据都是一次性消费，失败后必须重新登录（§10.1、§7.2.1）。 */
  const backToCredentials = (): void => {
    runtime.cancelMfa();
    runtime.cancelMfaSetup();
    setStep('credentials');
    setSetup(null);
    setRecoverySaved(false);
    setCode('');
  };

  const submit = handleSubmit(async (values) => {
    const parsed = loginSchema.safeParse(values);
    if (!parsed.success) {
      setError({ message: '请输入账号和密码', traceId: undefined });
      return;
    }
    setError(null);
    try {
      const result = await runtime.login(parsed.data.subject, parsed.data.password);
      if (result.kind === 'mfa') {
        setStep('mfa');
        return;
      }
      if (result.kind === 'mfaSetup') {
        try {
          const data = await runtime.beginMfaSetup();
          setSetup(data);
          setRecoverySaved(false);
          setStep('mfaSetup');
        } catch (cause) {
          backToCredentials();
          setError(toLoginError(cause, 'MFA 绑定初始化失败，请重新登录'));
        }
        return;
      }
      markAuthenticated();
    } catch (cause) {
      backToCredentials();
      setError(toLoginError(cause, '账号或密码错误'));
    }
  });

  const submitCode = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (code.trim() === '') {
      setError({ message: '请输入动态码或恢复码', traceId: undefined });
      return;
    }
    setError(null);
    setVerifying(true);
    try {
      if (step === 'mfaSetup') {
        await runtime.confirmMfaSetup(code.trim());
      } else {
        await runtime.verifyMfa(code.trim());
      }
      markAuthenticated();
    } catch (cause) {
      backToCredentials();
      setError(
        toLoginError(
          cause,
          step === 'mfaSetup'
            ? '动态码错误或绑定票据已失效，请重新登录'
            : '动态码错误或票据已失效，请重新登录',
        ),
      );
    } finally {
      setVerifying(false);
    }
  };

  const showCodeForm = step === 'mfa' || (step === 'mfaSetup' && recoverySaved);

  return (
    <div style={pageStyle}>
      <Card title="A 端运营后台" style={{ width: 420 }}>
        {step === 'mfa' ? (
          <Typography.Paragraph type="secondary">
            请输入身份验证器中的 6 位动态码，或使用一次性恢复码。
          </Typography.Paragraph>
        ) : null}
        {step === 'mfaSetup' ? (
          <Typography.Paragraph type="secondary">
            需要先完成 MFA 绑定才能进入后台。
          </Typography.Paragraph>
        ) : null}
        {step === 'credentials' ? (
          <Typography.Paragraph type="secondary">使用平台账号登录</Typography.Paragraph>
        ) : null}
        <ErrorAlert error={error} />
        {step === 'mfaSetup' && setup ? <MfaSetupPanel setup={setup} /> : null}
        {showCodeForm ? (
          <form onSubmit={(event) => void submitCode(event)} noValidate>
            <Space direction="vertical" size="middle" style={{ width: '100%' }}>
              <label htmlFor="mfa-code" style={fieldStyle}>
                动态码
              </label>
              <Input
                id="mfa-code"
                autoComplete="one-time-code"
                inputMode="numeric"
                value={code}
                onChange={(event) => {
                  setCode(event.target.value);
                }}
              />
              <Button type="primary" htmlType="submit" block loading={verifying}>
                验证
              </Button>
              <Button
                type="text"
                block
                onClick={() => {
                  backToCredentials();
                  setError(null);
                }}
              >
                返回登录
              </Button>
            </Space>
          </form>
        ) : null}
        {step === 'mfaSetup' && !recoverySaved ? (
          <Space direction="vertical" size="middle" style={{ width: '100%', marginTop: 16 }}>
            <Button type="primary" block onClick={() => setRecoverySaved(true)}>
              我已保存恢复码，继续
            </Button>
            <Button
              type="text"
              block
              onClick={() => {
                backToCredentials();
                setError(null);
              }}
            >
              返回登录
            </Button>
          </Space>
        ) : null}
        {step === 'credentials' ? (
          <form onSubmit={(event) => void submit(event)} noValidate>
            <Space direction="vertical" size="middle" style={{ width: '100%' }}>
              <label htmlFor="login-subject" style={fieldStyle}>
                账号
              </label>
              <Input id="login-subject" autoComplete="username" {...register('subject')} />
              <label htmlFor="login-password" style={fieldStyle}>
                密码
              </label>
              <Input.Password
                id="login-password"
                autoComplete="current-password"
                {...register('password')}
              />
              <Button type="primary" htmlType="submit" block loading={formState.isSubmitting}>
                登录
              </Button>
            </Space>
          </form>
        ) : null}
      </Card>
    </div>
  );
}

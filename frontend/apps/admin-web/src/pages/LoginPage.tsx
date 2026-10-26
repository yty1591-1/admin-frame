import type { MFASetupData } from '@admin-frame/api-client';
import { describeError, useI18n, type MessageKey } from '@admin-frame/i18n';
import {
  Alert,
  Button,
  Card,
  Divider,
  FormField,
  Input,
  PageLoading,
  Space,
  Typography,
} from '@admin-frame/ui';
import { useState, type CSSProperties, type FormEvent } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { Navigate, useSearchParams } from 'react-router';
import { z } from 'zod';
import { useAuth } from '../app/auth/auth-context';

const loginSchema = z.object({
  subject: z.string().trim().min(1),
  password: z.string().min(1),
});

type LoginValues = z.infer<typeof loginSchema>;

type LoginError = {
  messageKey: MessageKey;
  traceId: string | undefined;
};

type LoginFieldErrors = {
  subject?: string | undefined;
  password?: string | undefined;
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
function toLoginError(cause: unknown, unauthorizedKey: MessageKey): LoginError {
  const presentation = describeError(cause);
  if (presentation.code === 40100) {
    return { messageKey: unauthorizedKey, traceId: presentation.traceId };
  }
  return {
    messageKey:
      presentation.messageKey === 'error.unknown' ? 'auth.unknownFailure' : presentation.messageKey,
    traceId: presentation.traceId,
  };
}

const pageStyle: CSSProperties = {
  minHeight: '100vh',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: '#f5f5f5',
};

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
  const { t } = useI18n();
  if (!error) {
    return null;
  }
  return (
    <Alert
      type="error"
      showIcon
      style={{ marginBottom: 16 }}
      message={t(error.messageKey)}
      description={error.traceId ? t('common.traceId', { traceId: error.traceId }) : undefined}
    />
  );
}

/** 强制绑定第一步：展示密钥、otpauth URI 与一次性恢复码。 */
function MfaSetupPanel({ setup }: { setup: MFASetupData }) {
  const { t } = useI18n();

  return (
    <>
      <Typography.Paragraph type="secondary">{t('auth.mfaSetupIntro')}</Typography.Paragraph>
      <Space direction="vertical" size={4} style={{ width: '100%', marginBottom: 12 }}>
        <Typography.Text strong>{t('auth.mfaSecret')}</Typography.Text>
        <Typography.Text code copyable>
          {setup.secret}
        </Typography.Text>
        <Typography.Text strong>{t('auth.mfaOtpUri')}</Typography.Text>
        <Typography.Text code copyable ellipsis>
          {setup.otpauth_uri}
        </Typography.Text>
      </Space>
      <Divider style={{ margin: '12px 0' }} />
      <Typography.Paragraph type="warning" style={{ marginBottom: 0 }}>
        {t('auth.mfaRecoveryWarning')}
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
  const { t } = useI18n();
  const { status, runtime, markAuthenticated } = useAuth();
  const [searchParams] = useSearchParams();
  const returnTo = resolveReturnTo(searchParams.get('returnTo'));
  const [step, setStep] = useState<Step>('credentials');
  const [setup, setSetup] = useState<MFASetupData | null>(null);
  const [recoverySaved, setRecoverySaved] = useState(false);
  const [error, setError] = useState<LoginError | null>(null);
  const [fieldErrors, setFieldErrors] = useState<LoginFieldErrors>({});
  const [code, setCode] = useState('');
  const [verifying, setVerifying] = useState(false);
  const { control, handleSubmit, formState } = useForm<LoginValues>({
    defaultValues: { subject: '', password: '' },
  });

  if (status === 'booting') {
    return <PageLoading tip={t('auth.sessionRestoring')} />;
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
    setFieldErrors({});
  };

  const submit = handleSubmit(async (values) => {
    const parsed = loginSchema.safeParse(values);
    if (!parsed.success) {
      const paths = parsed.error.issues.map((issue) => issue.path[0]);
      setFieldErrors({
        ...(paths.includes('subject') ? { subject: t('auth.subjectRequired') } : {}),
        ...(paths.includes('password') ? { password: t('auth.passwordRequired') } : {}),
      });
      setError({ messageKey: 'auth.credentialsRequired', traceId: undefined });
      return;
    }
    setFieldErrors({});
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
          setError(toLoginError(cause, 'auth.mfaSetupInitFailed'));
        }
        return;
      }
      markAuthenticated();
    } catch (cause) {
      backToCredentials();
      setError(toLoginError(cause, 'auth.invalidCredentials'));
    }
  });

  const submitCode = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (code.trim() === '') {
      setError({ messageKey: 'auth.mfaCodeRequired', traceId: undefined });
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
        toLoginError(cause, step === 'mfaSetup' ? 'auth.mfaSetupInvalid' : 'auth.mfaInvalid'),
      );
    } finally {
      setVerifying(false);
    }
  };

  const showCodeForm = step === 'mfa' || (step === 'mfaSetup' && recoverySaved);

  return (
    <div style={pageStyle}>
      <Card title={t('common.appNameAdmin')} style={{ width: 420 }}>
        {step === 'mfa' ? (
          <Typography.Paragraph type="secondary">{t('auth.mfaPrompt')}</Typography.Paragraph>
        ) : null}
        {step === 'mfaSetup' ? (
          <Typography.Paragraph type="secondary">{t('auth.mfaSetupRequired')}</Typography.Paragraph>
        ) : null}
        {step === 'credentials' ? (
          <Typography.Paragraph type="secondary">{t('auth.loginIntro')}</Typography.Paragraph>
        ) : null}
        <ErrorAlert error={error} />
        {step === 'mfaSetup' && setup ? <MfaSetupPanel setup={setup} /> : null}
        {showCodeForm ? (
          <form onSubmit={(event) => void submitCode(event)} noValidate>
            <Space direction="vertical" size="middle" style={{ width: '100%' }}>
              <FormField
                label={t('auth.mfaCode')}
                htmlFor="mfa-code"
                required
                hint={t('auth.mfaCodeHint')}
              >
                <Input
                  id="mfa-code"
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  value={code}
                  onChange={(event) => {
                    setCode(event.target.value);
                  }}
                />
              </FormField>
              <Button type="primary" htmlType="submit" block loading={verifying}>
                {t('auth.verify')}
              </Button>
              <Button
                type="text"
                block
                onClick={() => {
                  backToCredentials();
                  setError(null);
                }}
              >
                {t('auth.backToLogin')}
              </Button>
            </Space>
          </form>
        ) : null}
        {step === 'mfaSetup' && !recoverySaved ? (
          <Space direction="vertical" size="middle" style={{ width: '100%', marginTop: 16 }}>
            <Button type="primary" block onClick={() => setRecoverySaved(true)}>
              {t('auth.mfaRecoverySaved')}
            </Button>
            <Button
              type="text"
              block
              onClick={() => {
                backToCredentials();
                setError(null);
              }}
            >
              {t('auth.backToLogin')}
            </Button>
          </Space>
        ) : null}
        {step === 'credentials' ? (
          <form onSubmit={(event) => void submit(event)} noValidate>
            <Space direction="vertical" size="middle" style={{ width: '100%' }}>
              <FormField
                label={t('auth.subject')}
                htmlFor="login-subject"
                required
                error={fieldErrors.subject}
              >
                <Controller
                  control={control}
                  name="subject"
                  render={({ field }) => (
                    <Input
                      id="login-subject"
                      autoComplete="username"
                      value={field.value}
                      onBlur={field.onBlur}
                      onChange={(event) => {
                        field.onChange(event);
                      }}
                    />
                  )}
                />
              </FormField>
              <FormField
                label={t('auth.password')}
                htmlFor="login-password"
                required
                error={fieldErrors.password}
              >
                <Controller
                  control={control}
                  name="password"
                  render={({ field }) => (
                    <Input.Password
                      id="login-password"
                      autoComplete="current-password"
                      value={field.value}
                      onBlur={field.onBlur}
                      onChange={(event) => {
                        field.onChange(event);
                      }}
                    />
                  )}
                />
              </FormField>
              <Button type="primary" htmlType="submit" block loading={formState.isSubmitting}>
                {t('auth.login')}
              </Button>
            </Space>
          </form>
        ) : null}
      </Card>
    </div>
  );
}

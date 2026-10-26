import { defaultTranslator } from '@admin-frame/i18n';

export function FatalConfigError({ message }: { message: string }) {
  return (
    <main style={{ padding: 48, fontFamily: 'system-ui, sans-serif' }}>
      <h1 style={{ fontSize: 20 }}>{defaultTranslator('common.fatalConfigTitle')}</h1>
      <p>{message}</p>
      <p style={{ color: '#666' }}>{defaultTranslator('common.fatalConfigHint')}</p>
    </main>
  );
}

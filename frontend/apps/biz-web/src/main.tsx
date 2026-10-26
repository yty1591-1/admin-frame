import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { defaultTranslator } from '@admin-frame/i18n';
import { loadRuntimeConfig } from '@admin-frame/shared';
import { App } from './app/App';
import { FatalConfigError } from './app/FatalConfigError';

const container = document.getElementById('root');
if (!container) {
  throw new Error(defaultTranslator('error.dev.missingRoot'));
}

const root = createRoot(container);

try {
  const config = loadRuntimeConfig(__CHANNEL__);
  root.render(
    <StrictMode>
      <App config={config} />
    </StrictMode>,
  );
} catch (error) {
  const message =
    error instanceof Error ? error.message : defaultTranslator('error.dev.runtimeConfigFailed');
  root.render(<FatalConfigError message={message} />);
}

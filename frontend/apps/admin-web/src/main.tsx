import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { loadRuntimeConfig } from '@admin-frame/shared';
import { App } from './app/App';
import { FatalConfigError } from './app/FatalConfigError';

const container = document.getElementById('root');
if (!container) {
  throw new Error('缺少 #root 挂载点');
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
  const message = error instanceof Error ? error.message : '运行期配置加载失败';
  root.render(<FatalConfigError message={message} />);
}

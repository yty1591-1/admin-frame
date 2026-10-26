import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';
import { sharedTestConfig } from './packages/config/src/vitest-base';

export default defineConfig({
  plugins: [react()],
  // 构建期常量在测试环境同样需要存在（HomePage 等读取 __CONTRACT_COMMIT__）。
  define: {
    __CHANNEL__: JSON.stringify('admin'),
    __APP_VERSION__: JSON.stringify('0.0.0-test'),
    __CONTRACT_COMMIT__: JSON.stringify('test-commit'),
  },
  test: sharedTestConfig,
});

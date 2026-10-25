import react from '@vitejs/plugin-react';
import { execSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

const rootDir = fileURLToPath(new URL('.', import.meta.url));

function readVersion(): string {
  const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')) as {
    version?: string;
  };
  return pkg.version ?? '0.0.0';
}

function readCommit(): string {
  try {
    return execSync('git rev-parse --short HEAD', { cwd: rootDir, encoding: 'utf8' }).trim();
  } catch {
    return 'unknown';
  }
}

export default defineConfig({
  plugins: [react()],
  define: {
    __CHANNEL__: JSON.stringify('biz'),
    __APP_VERSION__: JSON.stringify(readVersion()),
    __CONTRACT_COMMIT__: JSON.stringify(readCommit()),
  },
  server: {
    host: '127.0.0.1',
    port: 5172,
    strictPort: true,
    // 同源路径复刻，避免 CORS 与 Cookie 差异（§17.4）
    proxy: {
      '/biz': { target: 'http://127.0.0.1:8082', changeOrigin: false },
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 4174,
    strictPort: true,
  },
  build: {
    sourcemap: false,
    rollupOptions: {
      output: {
        // 只切分入口必然加载的依赖；antd 等按需组件保持随路由懒加载（避免首屏 preload 全量）。
        manualChunks(id: string) {
          if (!id.includes('node_modules')) {
            return undefined;
          }
          if (/node_modules\/(react|react-dom|scheduler)\//.test(id)) {
            return 'vendor-react';
          }
          if (/node_modules\/(react-router|@remix-run\/)/.test(id)) {
            return 'vendor-router';
          }
          if (/node_modules\/zod\//.test(id)) {
            return 'vendor-zod';
          }
          if (/node_modules\/(@tanstack|zustand)\//.test(id)) {
            return 'vendor-data';
          }
          return undefined;
        },
      },
    },
  },
});

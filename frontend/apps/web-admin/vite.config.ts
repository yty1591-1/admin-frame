import { defineConfig } from '@vben/vite-config';

export default defineConfig(async () => {
  return {
    application: {},
    vite: {
      server: {
        proxy: {
          // 本地直连 admin-frame A 端；/admin 前缀原样转发，不做 rewrite
          '/admin': {
            changeOrigin: true,
            target: 'http://localhost:8081',
          },
        },
      },
    },
  };
});

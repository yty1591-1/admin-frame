import { defineConfig } from '@vben/vite-config';

export default defineConfig(async () => {
  return {
    application: {},
    vite: {
      server: {
        proxy: {
          // 本地直连 admin-frame B 端；/biz 前缀原样转发，不做 rewrite。
          // B 端是独立的 biz-api 服务（configs/biz.yaml → :8082），与 A 端 :8081 分离。
          '/biz': {
            changeOrigin: true,
            target: 'http://localhost:8082',
          },
        },
      },
    },
  };
});

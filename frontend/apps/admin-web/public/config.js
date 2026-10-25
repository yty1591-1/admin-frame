/* 运行期配置（非敏感）。部署时由静态服务覆盖，禁止写入密钥（FE-16）。 */
window.__APP_CONFIG__ = {
  apiBaseUrl: '',
  env: 'local',
  channel: 'admin',
  features: {},
  monitoring: {
    dsn: null,
    sampleRate: 0,
  },
};

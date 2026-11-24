import type { ApplicationPluginOptions } from '../typing';

import { existsSync } from 'node:fs';
import { join } from 'node:path';

import { fs } from '@vben/node-utils';

import dotenv from 'dotenv';

const getBoolean = (value: string | undefined) => value === 'true';

const getString = (value: string | undefined, fallback: string) =>
  value ?? fallback;

const getNumber = (value: string | undefined, fallback: number) =>
  Number(value) || fallback;

/**
 * 允许出现在 `.env*` 中的构建期变量白名单（FE-B-05）。
 *
 * 新增变量必须先登记在这里：未登记的 VITE_* 会在构建配置阶段直接失败，
 * 防止 `VITE_APP_STORE_SECURE_KEY` 之类的键被随手塞进 `.env` 后带入产物。
 * 前三个由 `viteMetadataPlugin` 注入，不需要在 `.env` 中维护。
 */
const DECLARED_ENV_KEYS = new Set([
  'VITE_APP_BUILD_TIME',
  'VITE_APP_GIT_SHA',
  'VITE_APP_NAMESPACE',
  'VITE_APP_STORE_SECURE_KEY',
  'VITE_APP_TITLE',
  'VITE_APP_VERSION',
  'VITE_ARCHIVER',
  'VITE_BASE',
  'VITE_COMPRESS',
  'VITE_DEVTOOLS',
  'VITE_GLOB_API_URL',
  'VITE_GLOB_AUTH_DINGDING_CLIENT_ID',
  'VITE_GLOB_AUTH_DINGDING_CORP_ID',
  'VITE_INJECT_APP_LOADING',
  'VITE_PORT',
  'VITE_PWA',
  'VITE_ROUTER_HISTORY',
  'VITE_VISUALIZER',
]);

/**
 * 校验 `.env*` 中的变量都在白名单内；未声明变量直接抛错，阻断构建。
 */
function assertDeclaredEnv(keys: Iterable<string>): void {
  const undeclared = [...keys].filter((key) => !DECLARED_ENV_KEYS.has(key));
  if (undeclared.length > 0) {
    throw new Error(
      `未声明的构建期环境变量：${undeclared.join(', ')}。` +
        '请登记到 frontend/internal/vite-config/src/utils/env.ts 的 DECLARED_ENV_KEYS，' +
        '或从 .env 文件移除（FE-B-05）。',
    );
  }
}

/**
 * 获取当前环境下生效的配置文件名
 */
function getConfFiles() {
  const script = process.env.npm_lifecycle_script as string;
  const reg = /--mode ([\d_a-z]+)/;
  const result = reg.exec(script);
  let mode = 'production';
  if (result) {
    mode = result[1] as string;
  }
  return ['.env', '.env.local', `.env.${mode}`, `.env.${mode}.local`];
}

/**
 * Get the environment variables starting with the specified prefix
 * @param match prefix
 * @param confFiles ext
 */
async function loadEnv<T = Record<string, string>>(
  match = 'VITE_GLOB_',
  confFiles = getConfFiles(),
) {
  let envConfig = {};

  for (const confFile of confFiles) {
    try {
      const confFilePath = join(process.cwd(), confFile);
      if (existsSync(confFilePath)) {
        const envPath = await fs.readFile(confFilePath, {
          encoding: 'utf8',
        });
        const env = dotenv.parse(envPath);
        envConfig = { ...envConfig, ...env };
      }
    } catch (error) {
      console.error(`Error while parsing ${confFile}`, error);
    }
  }
  const reg = new RegExp(`^(${match})`);
  Object.keys(envConfig).forEach((key) => {
    if (!reg.test(key)) {
      Reflect.deleteProperty(envConfig, key);
    }
  });
  return envConfig as T;
}

async function loadAndConvertEnv(
  match = 'VITE_',
  confFiles = getConfFiles(),
): Promise<
  Partial<ApplicationPluginOptions> & {
    appTitle: string;
    base: string;
    port: number;
  }
> {
  const envConfig = await loadEnv(match, confFiles);
  assertDeclaredEnv(Object.keys(envConfig));

  const {
    VITE_APP_TITLE,
    VITE_ARCHIVER,
    VITE_BASE,
    VITE_COMPRESS,
    VITE_DEVTOOLS,
    VITE_INJECT_APP_LOADING,
    VITE_PORT,
    VITE_PWA,
    VITE_VISUALIZER,
  } = envConfig;

  const compressTypes = (VITE_COMPRESS ?? '')
    .split(',')
    .filter((item) => item === 'brotli' || item === 'gzip');

  return {
    appTitle: getString(VITE_APP_TITLE, 'Vben Admin'),
    archiver: getBoolean(VITE_ARCHIVER),
    base: getString(VITE_BASE, '/'),
    compress: compressTypes.length > 0,
    compressTypes,
    devtools: getBoolean(VITE_DEVTOOLS),
    injectAppLoading: getBoolean(VITE_INJECT_APP_LOADING),
    port: getNumber(VITE_PORT, 5173),
    pwa: getBoolean(VITE_PWA),
    visualizer: getBoolean(VITE_VISUALIZER),
  };
}

export { assertDeclaredEnv, loadAndConvertEnv, loadEnv };

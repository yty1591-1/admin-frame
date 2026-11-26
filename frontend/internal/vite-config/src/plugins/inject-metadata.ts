import type { PluginOption } from 'vite';

import { execFileSync } from 'node:child_process';

import {
  dateUtil,
  findMonorepoRoot,
  getPackages,
  readPackageJSON,
} from '@vben/node-utils';

import { readWorkspaceManifest } from '@pnpm/workspace.read-manifest';

function resolvePackageVersion(
  pkgsMeta: Record<string, string>,
  name: string,
  value: string,
  catalog: Record<string, string>,
) {
  if (value.includes('catalog:')) {
    return catalog[name];
  }

  if (value.includes('workspace')) {
    return pkgsMeta[name];
  }

  return value;
}

async function resolveMonorepoDependencies() {
  const { packages } = await getPackages();
  const manifest = await readWorkspaceManifest(findMonorepoRoot());
  const catalog = manifest?.catalog || {};

  const resultDevDependencies: Record<string, string | undefined> = {};
  const resultDependencies: Record<string, string | undefined> = {};
  const pkgsMeta: Record<string, string> = {};

  for (const { packageJson } of packages) {
    pkgsMeta[packageJson.name] = packageJson.version;
  }

  for (const { packageJson } of packages) {
    const { dependencies = {}, devDependencies = {} } = packageJson;
    for (const [key, value] of Object.entries(dependencies)) {
      resultDependencies[key] = resolvePackageVersion(
        pkgsMeta,
        key,
        value,
        catalog,
      );
    }
    for (const [key, value] of Object.entries(devDependencies)) {
      resultDevDependencies[key] = resolvePackageVersion(
        pkgsMeta,
        key,
        value,
        catalog,
      );
    }
  }
  return {
    dependencies: resultDependencies,
    devDependencies: resultDevDependencies,
  };
}

/**
 * 解析构建产物的提交 SHA。
 *
 * 优先使用 CI 注入的提交号（发布流水线可追溯），本地回退到 `git rev-parse`；
 * 两者都不可用时返回 `unknown`，由 CI 的构建元数据断言拦截（FE-B-01）。
 */
function resolveGitSha(cwd: string): string {
  const fromEnv = process.env.VITE_APP_GIT_SHA;
  if (fromEnv && /^[\da-f]{40}$/i.test(fromEnv)) {
    return fromEnv.toLowerCase();
  }
  const fromCi =
    process.env.GITHUB_SHA ??
    process.env.CI_COMMIT_SHA ??
    process.env.VERCEL_GIT_COMMIT_SHA;
  if (fromCi && /^[\da-f]{40}$/i.test(fromCi)) {
    return fromCi.toLowerCase();
  }
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], {
      cwd,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    })
      .trim()
      .toLowerCase();
  } catch {
    return 'unknown';
  }
}

/**
 * 用于注入项目信息
 */
async function viteMetadataPlugin(
  root = process.cwd(),
): Promise<PluginOption | undefined> {
  const { author, description, homepage, license, version } =
    await readPackageJSON(root);

  const buildTime = dateUtil().format('YYYY-MM-DD HH:mm:ss');
  const buildTimeIso = new Date().toISOString();
  const gitSha = resolveGitSha(root);

  return {
    async config() {
      const { dependencies, devDependencies } =
        await resolveMonorepoDependencies();

      const isAuthorObject = typeof author === 'object';
      const authorName = isAuthorObject ? author.name : author;
      const authorEmail = isAuthorObject ? author.email : null;
      const authorUrl = isAuthorObject ? author.url : null;

      return {
        define: {
          __VBEN_ADMIN_METADATA__: JSON.stringify({
            authorEmail,
            authorName,
            authorUrl,
            buildTime,
            buildTimeIso,
            dependencies,
            description,
            devDependencies,
            gitSha,
            homepage,
            license,
            version,
          }),
          'import.meta.env.VITE_APP_BUILD_TIME': JSON.stringify(buildTimeIso),
          'import.meta.env.VITE_APP_GIT_SHA': JSON.stringify(gitSha),
          'import.meta.env.VITE_APP_VERSION': JSON.stringify(version),
        },
      };
    },
    enforce: 'post',
    name: 'vite:inject-metadata',
    transformIndexHtml() {
      // 线上排障时 curl 首页即可拿到版本与提交号，无需登录；
      // 不包含任何构建期环境变量值（FE-B-01）。
      return [
        {
          attrs: { content: version, name: 'x-app-version' },
          injectTo: 'head',
          tag: 'meta',
        },
        {
          attrs: { content: gitSha, name: 'x-app-git-sha' },
          injectTo: 'head',
          tag: 'meta',
        },
        {
          attrs: { content: buildTimeIso, name: 'x-app-build-time' },
          injectTo: 'head',
          tag: 'meta',
        },
      ];
    },
  };
}

export { viteMetadataPlugin };

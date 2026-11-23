/**
 * 配置中心首次安装 API（ADR-0017）。
 *
 * 经 admin-api `/admin/v1/install/*` 代理到 config-api（携带 mTLS），
 * 浏览器不直连 config-api，避免 prod HTTPS/mTLS 下的 400。
 * POST 须带 Idempotency-Key（经 admin-api 平台幂等中间件）。
 */

import { ensureIdempotencyHeader } from '#/api/idempotency';

export type InstallState = {
  env: string;
  status: 'failed' | 'installing' | 'pending' | 'ready' | string;
  owner?: string;
  lease_until?: string;
  version: number;
  last_error?: string;
  updated_at?: string;
  completed_at?: string;
};

type Envelope<T> = { code: number; message?: string; data: T };

function installBase(): string {
  // 与 Vite 代理 / admin 同源前缀一致；勿直连 :8085。
  const raw = import.meta.env.VITE_GLOB_API_URL as string | undefined;
  const api = (raw || '/admin/v1').replace(/\/$/, '');
  // VITE_GLOB_API_URL 形如 /admin/v1 → 安装路径挂在同前缀下
  if (api.endsWith('/v1')) {
    return `${api.slice(0, -3)}/v1/install`;
  }
  return `${api}/install`;
}

async function installFetch<T>(
  path: string,
  init?: RequestInit & { token?: string },
): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set('Accept', 'application/json');
  if (init?.token) {
    headers.set('X-Config-Install-Token', init.token);
  }
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  ensureIdempotencyHeader(headers, init?.method);
  const resp = await fetch(`${installBase()}${path}`, {
    ...init,
    headers,
  });
  const text = await resp.text();
  let json: Envelope<T>;
  try {
    json = JSON.parse(text) as Envelope<T>;
  } catch {
    throw new Error(
      text.trim() || `install api http ${resp.status} (non-json)`,
    );
  }
  if (!resp.ok || (json.code !== undefined && json.code !== 0)) {
    throw new Error(json.message || `install api http ${resp.status}`);
  }
  return json.data;
}

export function fetchInstallStatus() {
  return installFetch<InstallState>('/status');
}

export function beginInstall(token: string, owner?: string) {
  return installFetch<InstallState>('/begin', {
    method: 'POST',
    token,
    body: JSON.stringify({ owner: owner || '' }),
  });
}

export function applyInstall(
  token: string,
  owner: string,
  overrides: Record<string, string> = {},
) {
  return installFetch<{
    namespaces: number;
    item_count: number;
    state: InstallState;
  }>('/apply', {
    method: 'POST',
    token,
    body: JSON.stringify({ owner, overrides }),
  });
}

export function completeInstall(token: string, owner: string) {
  return installFetch<InstallState>('/complete', {
    method: 'POST',
    token,
    body: JSON.stringify({ owner }),
  });
}

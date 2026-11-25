/**
 * 配置中心安装向导 API（ADR-0017 / ADR-0018）。
 *
 * 经 admin-api `/admin/v1/install/*` 代理到 config-api（携带 mTLS），
 * 浏览器不直连 config-api，避免 prod HTTPS/mTLS 下的 400。
 * POST 须带 Idempotency-Key（经 admin-api 平台幂等中间件）；
 * `schema` 与写操作都要 setup token（`X-Config-Install-Token`）。
 */

import { ensureIdempotencyHeader } from '#/api/idempotency';

/** 首装 vs 重配置：ready 之后一律按 repair 展示。 */
export type InstallMode = 'first_install' | 'repair';

export type InstallStatus = 'failed' | 'installing' | 'pending' | 'ready' | string;

/** 未满足的阻塞键（服务端与进程同源判定）。 */
export type InstallGap = {
  key: string;
  namespace: string;
  reason?: string;
};

export type InstallState = {
  completed_at?: string;
  env: string;
  last_error?: string;
  lease_until?: string;
  mode: InstallMode;
  owner?: string;
  repair_keys: string[];
  status: InstallStatus;
  updated_at?: string;
  version: number;
};

/** status 响应在 InstallState 之上追加阻塞键统计。 */
export type InstallStatusData = InstallState & {
  blocking_count: number;
  gaps: InstallGap[];
};

/** 默认值来源：代码声明 / 环境叠加层 / 无默认值。 */
export type SchemaDefaultSource = 'code_default' | 'dev_default' | 'none' | 'prod_default' | string;

export type SchemaDefault = {
  source: SchemaDefaultSource;
  value: string;
};

/** 敏感键的生成规则（不含任何密钥值）。 */
export type SchemaSecretRule = {
  algorithm: string;
  auto_generate: boolean;
  encoding: string;
  max_len: number;
  min_len: number;
  prefix?: string;
};

export type SchemaField = {
  /** 是否阻塞启动（进程缺它就起不来）。 */
  blocking: boolean;
  default: SchemaDefault;
  deprecated: boolean;
  description: string;
  enum?: string[];
  group: string;
  group_title: string;
  hot_whitelist: boolean;
  key: string;
  label: string;
  max_int?: number;
  min_int?: number;
  namespace: string;
  order: number;
  owner: string;
  pattern?: string;
  reload_mode: string;
  required: boolean;
  secret?: SchemaSecretRule;
  /** 是否敏感：取值不进配置快照，由密钥服务生成。 */
  sensitive: boolean;
  value_type: string;
  widget: string;
};

export type SchemaNamespace = {
  blocking_count: number;
  key_count: number;
  namespace: string;
  order: number;
  title: string;
};

export type SchemaGap = {
  key: string;
  label: string;
  namespace: string;
  reason: string;
};

export type InstallSchema = {
  blocking_count: number;
  blocking_keys: SchemaGap[];
  env: string;
  fields: SchemaField[];
  mode: InstallMode;
  namespaces: SchemaNamespace[];
  status: InstallStatus;
};

export type ApplyResult = {
  item_count: number;
  namespaces: number;
  remaining_gaps: InstallGap[];
  state: InstallState;
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

async function installFetch<T>(path: string, init?: RequestInit & { token?: string }): Promise<T> {
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
    throw new Error(text.trim() || `install api http ${resp.status} (non-json)`);
  }
  if (!resp.ok || (json.code !== undefined && json.code !== 0)) {
    throw new Error(json.message || `install api http ${resp.status}`);
  }
  return json.data;
}

export function fetchInstallStatus() {
  return installFetch<InstallStatusData>('/status');
}

/**
 * 拉取向导渲染元数据。
 *
 * 省略 token 时服务端返回 401——首装阶段没有登录态，只能靠 setup token。
 */
export function fetchInstallSchema(token: string) {
  return installFetch<InstallSchema>('/schema', { token });
}

export function beginInstall(
  token: string,
  opts: {
    /** 运维声明的额外阻塞键（`namespace/key`），只在 repair 有效。 */
    declaredKeys?: string[];
    owner?: string;
    reason?: InstallMode;
  } = {},
) {
  const body: Record<string, unknown> = { owner: opts.owner || '' };
  if (opts.reason) {
    body.reason = opts.reason;
  }
  if (opts.declaredKeys && opts.declaredKeys.length > 0) {
    body.declared_keys = opts.declaredKeys;
  }
  return installFetch<InstallState>('/begin', {
    body: JSON.stringify(body),
    method: 'POST',
    token,
  });
}

export function applyInstall(
  token: string,
  opts: {
    owner: string;
    /** 按 namespace 分组的配置值；空串表示「用默认值」。 */
    values?: Record<string, Record<string, string>>;
    /** 兼容扁平键值（旧调用方）。 */
    overrides?: Record<string, string>;
  },
) {
  const body: Record<string, unknown> = { owner: opts.owner };
  if (opts.values && Object.keys(opts.values).length > 0) {
    body.values = opts.values;
  }
  if (opts.overrides && Object.keys(opts.overrides).length > 0) {
    body.overrides = opts.overrides;
  }
  return installFetch<ApplyResult>('/apply', {
    body: JSON.stringify(body),
    method: 'POST',
    token,
  });
}

export function completeInstall(token: string, owner: string) {
  return installFetch<InstallState>('/complete', {
    body: JSON.stringify({ owner }),
    method: 'POST',
    token,
  });
}

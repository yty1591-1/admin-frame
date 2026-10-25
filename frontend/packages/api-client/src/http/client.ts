import type { Channel } from '@admin-frame/shared';
import { z } from 'zod';
import { AppError, codeFromStatus, kindFromCode, kindFromStatus } from '../errors/app-error';

export type HttpClientOptions = {
  /** 空字符串表示同源；运行时配置优先（§9.2）。 */
  baseUrl: string;
  channel: Channel;
  getAccessToken?: (() => string | null) | undefined;
  /** 40100 时触发上层单飞刷新（由 auth 包装配）；返回 null 表示刷新失败。 */
  refreshAccessToken?: (() => Promise<string | null>) | undefined;
  /** 40100 的同步通知；保留给需要登出跳转的装配层。 */
  onUnauthorized?: (() => void) | undefined;
  /** 40300 的同步通知，装配层据此强制刷新权限快照（§7.2）。 */
  onForbidden?: ((path: string) => void) | undefined;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
};

export type RequestOptions = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  query?: Readonly<Record<string, string | number | boolean | undefined>>;
  body?: unknown;
  auth?: boolean;
  signal?: AbortSignal | undefined;
  idempotencyKey?: string | undefined;
  requireIdempotencyKey?: boolean;
  timeoutMs?: number;
  retry?: 'safe' | 'none';
  /** 成功响应的 data 载荷校验；不传表示无数据（204/void）。 */
  schema?: z.ZodType;
};

type Envelope = {
  code: number;
  message: string;
  data: unknown;
  traceId: string | undefined;
  httpStatus: number;
};

const DEFAULT_TIMEOUT_MS = 15_000;
const MAX_RETRIES = 2;

const envelopeSchema = z.object({
  code: z.number().int(),
  message: z.string(),
  data: z.unknown().optional(),
  trace_id: z.string().optional(),
});

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

function extractFieldErrors(payload: unknown): Record<string, string> | undefined {
  if (typeof payload !== 'object' || payload === null) {
    return undefined;
  }
  const candidate = (payload as { field_errors?: unknown }).field_errors;
  if (typeof candidate !== 'object' || candidate === null) {
    return undefined;
  }
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(candidate)) {
    if (typeof value === 'string') {
      result[key] = value;
    }
  }
  return Object.keys(result).length > 0 ? result : undefined;
}

/**
 * 唯一 HTTP 出口（FE-06）：超时、重试、信封解包、错误归一、trace_id 保留。
 * 业务代码不得直接使用 fetch/axios。
 */
export class HttpClient {
  readonly channel: Channel;

  private readonly baseUrl: string;
  private readonly timeoutMs: number;
  private readonly fetchImpl: typeof fetch;
  private readonly getAccessToken: (() => string | null) | undefined;
  private readonly refreshAccessToken: (() => Promise<string | null>) | undefined;
  private readonly onUnauthorized: (() => void) | undefined;
  private readonly onForbidden: ((path: string) => void) | undefined;

  constructor(options: HttpClientOptions) {
    this.channel = options.channel;
    this.baseUrl = options.baseUrl;
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);
    this.getAccessToken = options.getAccessToken;
    this.refreshAccessToken = options.refreshAccessToken;
    this.onUnauthorized = options.onUnauthorized;
    this.onForbidden = options.onForbidden;
  }

  /** 成功时返回契约类型 T；失败统一抛 AppError。 */
  async request<T>(path: string, options: RequestOptions): Promise<T> {
    const envelope = await this.execute(path, options);
    const schema = options.schema;
    if (!schema) {
      throw new AppError({
        kind: 'server',
        code: 50000,
        message: '缺少响应校验 schema',
        httpStatus: envelope.httpStatus,
        traceId: envelope.traceId,
      });
    }
    const parsed = schema.safeParse(envelope.data);
    if (!parsed.success) {
      throw new AppError({
        kind: 'server',
        code: 50000,
        message: '响应数据与契约不一致',
        httpStatus: envelope.httpStatus,
        traceId: envelope.traceId,
      });
    }
    // 契约生成类型是唯一来源（FE-08），运行时已完成形状校验。
    return parsed.data as T;
  }

  async requestVoid(path: string, options: RequestOptions = {}): Promise<void> {
    await this.execute(path, options);
  }

  private async execute(path: string, options: RequestOptions): Promise<Envelope> {
    if (
      options.requireIdempotencyKey === true &&
      (options.idempotencyKey === undefined || options.idempotencyKey === '')
    ) {
      throw new AppError({
        kind: 'validation',
        code: 40000,
        message: '写操作必须显式提供 Idempotency-Key（§7.3）',
      });
    }

    const attempts = options.retry === 'safe' ? MAX_RETRIES + 1 : 1;
    let lastError: AppError | undefined;

    for (let attempt = 0; attempt < attempts; attempt += 1) {
      try {
        return await this.attempt(path, options);
      } catch (error) {
        const appError =
          error instanceof AppError
            ? error
            : new AppError({ kind: 'unknown', code: 0, message: '未知错误', cause: error });
        const retriable =
          appError.kind === 'network' ||
          appError.kind === 'timeout' ||
          (appError.httpStatus !== undefined && appError.httpStatus >= 500);
        if (!retriable || attempt === attempts - 1) {
          throw appError;
        }
        lastError = appError;
        await sleep(100 * 2 ** attempt + Math.random() * 50);
      }
    }
    throw lastError ?? new AppError({ kind: 'unknown', code: 0, message: '请求失败' });
  }

  private async attempt(
    path: string,
    options: RequestOptions,
    allowRefresh = true,
  ): Promise<Envelope> {
    const url = this.buildUrl(path, options.query);
    const controller = new AbortController();
    const timeoutMs = options.timeoutMs ?? this.timeoutMs;
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, timeoutMs);

    const external = options.signal;
    const onExternalAbort = (): void => {
      controller.abort();
    };
    external?.addEventListener('abort', onExternalAbort, { once: true });

    const headers: Record<string, string> = { Accept: 'application/json' };
    if (options.body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    if (options.auth !== false) {
      const token = this.getAccessToken?.() ?? null;
      if (token) {
        headers.Authorization = `Bearer ${token}`;
      }
    }
    if (options.idempotencyKey) {
      headers['Idempotency-Key'] = options.idempotencyKey;
    }

    try {
      const init: RequestInit = {
        method: options.method ?? 'GET',
        headers,
        signal: controller.signal,
      };
      if (options.body !== undefined) {
        init.body = JSON.stringify(options.body);
      }
      const response = await this.fetchImpl(url, init);
      return await this.parseResponse(response, path, options, allowRefresh);
    } catch (error) {
      if (error instanceof AppError) {
        throw error;
      }
      if (timedOut) {
        throw new AppError({ kind: 'timeout', code: 0, message: '请求超时', cause: error });
      }
      if (external?.aborted) {
        throw new AppError({ kind: 'network', code: 0, message: '请求已取消', cause: error });
      }
      throw new AppError({ kind: 'network', code: 0, message: '网络连接失败', cause: error });
    } finally {
      clearTimeout(timer);
      external?.removeEventListener('abort', onExternalAbort);
    }
  }

  private buildUrl(path: string, query: RequestOptions['query']): string {
    const origin =
      this.baseUrl !== ''
        ? this.baseUrl
        : typeof window !== 'undefined'
          ? window.location.origin
          : 'http://localhost';
    const url = new URL(path, origin);
    if (query) {
      for (const [key, value] of Object.entries(query)) {
        if (value !== undefined) {
          url.searchParams.set(key, String(value));
        }
      }
    }
    return url.toString();
  }

  private async parseResponse(
    response: Response,
    path: string,
    options: RequestOptions,
    allowRefresh: boolean,
  ): Promise<Envelope> {
    if (response.status === 204) {
      return {
        code: 0,
        message: 'ok',
        data: undefined,
        traceId: undefined,
        httpStatus: 204,
      };
    }

    const text = await response.text();
    let payload: unknown;
    try {
      payload = text === '' ? {} : JSON.parse(text);
    } catch (error) {
      throw new AppError({
        kind: 'server',
        code: 50000,
        message: '响应不是合法 JSON',
        httpStatus: response.status,
        cause: error,
      });
    }

    const envelope = envelopeSchema.safeParse(payload);
    if (!envelope.success) {
      throw new AppError({
        kind: 'server',
        code: 50000,
        message: '响应缺少统一信封',
        httpStatus: response.status,
      });
    }

    const { code, message, trace_id: traceId } = envelope.data;
    if (!response.ok || code !== 0) {
      const errorCode = code !== 0 ? code : codeFromStatus(response.status);
      if (code === 40100) {
        this.onUnauthorized?.();
        if (allowRefresh && options.auth !== false) {
          const refreshed = await this.tryRefresh();
          if (refreshed) {
            // 单飞刷新成功后仅重放当前请求一次，避免刷新令牌重放（§10.3）。
            return this.attempt(path, options, false);
          }
        }
      }
      if (errorCode === 40300) {
        // 权限可能刚被收回：通知装配层刷新快照，而不是在这里自动重试（§7.2）。
        this.onForbidden?.(path);
      }
      throw new AppError({
        kind: code !== 0 ? kindFromCode(code) : kindFromStatus(response.status),
        code: errorCode,
        message,
        httpStatus: response.status,
        traceId,
        fieldErrors: extractFieldErrors(payload),
      });
    }

    return {
      code,
      message,
      data: envelope.data.data,
      traceId,
      httpStatus: response.status,
    };
  }

  /** 刷新失败即视为会话失效；由 RefreshManager 负责清理与通知。 */
  private async tryRefresh(): Promise<string | null> {
    if (!this.refreshAccessToken) {
      return null;
    }
    try {
      return await this.refreshAccessToken();
    } catch {
      return null;
    }
  }
}

import { z } from 'zod';

export type Channel = 'admin' | 'biz';

export type AppRuntimeEnv = 'local' | 'dev' | 'staging' | 'prod';

/** 运行期配置（/config.js），构建期只注入非敏感默认值。 */
export type AppRuntimeConfig = {
  apiBaseUrl: string;
  env: AppRuntimeEnv;
  channel: Channel;
  features: Readonly<Record<string, boolean>>;
  monitoring: {
    dsn: string | null;
    sampleRate: number;
    /** 上报版本标识；缺省为 null，由装配层回退构建期 __APP_VERSION__。 */
    release: string | null;
  };
};

export class RuntimeConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RuntimeConfigError';
  }
}

const runtimeConfigSchema = z.object({
  apiBaseUrl: z.string(),
  env: z.enum(['local', 'dev', 'staging', 'prod']),
  channel: z.enum(['admin', 'biz']),
  features: z.record(z.string(), z.boolean()).optional(),
  monitoring: z
    .object({
      dsn: z.string().nullable().optional(),
      sampleRate: z.number().min(0).max(1).optional(),
      release: z.string().nullable().optional(),
    })
    .optional(),
});

function readWindowConfig(): unknown {
  if (typeof window === 'undefined') {
    return undefined;
  }
  return (window as unknown as { __APP_CONFIG__?: unknown }).__APP_CONFIG__;
}

/**
 * 校验运行期配置并断言端别（§6 Channel 固定、§17.1 配置分层）。
 * 失败时抛出 RuntimeConfigError，由入口渲染致命错误页（fail-closed）。
 */
export function loadRuntimeConfig(
  expectedChannel: Channel,
  source: unknown = readWindowConfig(),
): AppRuntimeConfig {
  const parsed = runtimeConfigSchema.safeParse(source);
  if (!parsed.success) {
    const fields = parsed.error.issues.map((issue) => issue.path.join('.')).join(', ');
    throw new RuntimeConfigError(`运行期配置无效（${fields || 'unknown'}）`);
  }
  if (parsed.data.channel !== expectedChannel) {
    throw new RuntimeConfigError(
      `端别不一致：构建期 ${expectedChannel}，运行期 ${parsed.data.channel}`,
    );
  }
  return {
    apiBaseUrl: parsed.data.apiBaseUrl,
    env: parsed.data.env,
    channel: parsed.data.channel,
    features: parsed.data.features ?? {},
    monitoring: {
      dsn: parsed.data.monitoring?.dsn ?? null,
      sampleRate: parsed.data.monitoring?.sampleRate ?? 0,
      release: parsed.data.monitoring?.release ?? null,
    },
  };
}

/**
 * 脱敏日志（S3 / 前端架构 §12.2）。
 * 默认只写入环形缓冲，不输出控制台；上报通道由 OTel transport 注入。
 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export type LogFields = {
  event: string;
  traceId?: string | undefined;
  route?: string | undefined;
  code?: number | undefined;
  kind?: string | undefined;
  source?: string | undefined;
  durationMs?: number | undefined;
  clientGenerated?: boolean | undefined;
};

export type LogRecord = {
  level: LogLevel;
  event: string;
  /** ISO 8601。 */
  time: string;
  fields: Readonly<Record<string, string | number | boolean>>;
};

export type LogSink = (record: LogRecord) => void;

export type LoggerOptions = {
  sink?: LogSink | undefined;
  level?: LogLevel | undefined;
  bufferSize?: number | undefined;
};

const LEVEL_WEIGHT: Readonly<Record<LogLevel, number>> = {
  debug: 10,
  info: 20,
  warn: 30,
  error: 40,
};

const DEFAULT_LEVEL: LogLevel = 'info';
const DEFAULT_BUFFER_SIZE = 50;
const MAX_STRING_LENGTH = 200;

const SAFE_FIELD_KEYS = new Set([
  'event',
  'traceId',
  'route',
  'code',
  'kind',
  'source',
  'durationMs',
  'clientGenerated',
]);

/** 敏感键即使未来进入白名单也直接丢弃（§12.2）。 */
const SENSITIVE_KEY =
  /password|passwd|token|secret|cookie|authorization|refresh|mfa|credential|session/i;

type LoggerState = {
  level: LogLevel;
  sink: LogSink | null;
  bufferSize: number;
  buffer: LogRecord[];
  emitting: boolean;
};

const state: LoggerState = {
  level: DEFAULT_LEVEL,
  sink: null,
  bufferSize: DEFAULT_BUFFER_SIZE,
  buffer: [],
  emitting: false,
};

function isLogLevel(value: unknown): value is LogLevel {
  return value === 'debug' || value === 'info' || value === 'warn' || value === 'error';
}

function sanitizeValue(value: unknown): string | number | boolean | undefined {
  if (typeof value === 'string') {
    return value.length > MAX_STRING_LENGTH ? value.slice(0, MAX_STRING_LENGTH) : value;
  }
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : undefined;
  }
  if (typeof value === 'boolean') {
    return value;
  }
  return value === undefined || value === null ? undefined : '[object]';
}

function toFields(input: LogFields): Readonly<Record<string, string | number | boolean>> {
  const fields: Record<string, string | number | boolean> = {};
  for (const [key, value] of Object.entries(input)) {
    if (!SAFE_FIELD_KEYS.has(key) || SENSITIVE_KEY.test(key)) {
      continue;
    }
    const safe = sanitizeValue(value);
    if (safe !== undefined) {
      fields[key] = safe;
    }
  }
  return fields;
}

function write(level: LogLevel, input: LogFields): void {
  if (LEVEL_WEIGHT[level] < LEVEL_WEIGHT[state.level]) {
    return;
  }
  const event = typeof input.event === 'string' && input.event !== '' ? input.event : 'unknown';
  const record: LogRecord = {
    level,
    event,
    time: new Date().toISOString(),
    fields: toFields(input),
  };

  state.buffer.push(record);
  while (state.buffer.length > state.bufferSize) {
    state.buffer.shift();
  }

  if (state.sink === null || state.emitting) {
    return;
  }
  state.emitting = true;
  try {
    state.sink(record);
  } catch {
    // 上报失败不得影响页面；同时避免 sink 内递归写日志时无限循环。
  } finally {
    state.emitting = false;
  }
}

/**
 * 配置日志出口（装配层调用）。未传 sink 时保持默认「仅缓冲」。
 * 模块加载即具备默认配置，保证根错误边界在 Provider 挂载前也可记录。
 */
export function configureLogger(options: LoggerOptions = {}): void {
  if (isLogLevel(options.level)) {
    state.level = options.level;
  }
  if (options.sink !== undefined) {
    state.sink = options.sink;
  }
  if (
    options.bufferSize !== undefined &&
    Number.isInteger(options.bufferSize) &&
    options.bufferSize > 0
  ) {
    state.bufferSize = options.bufferSize;
    while (state.buffer.length > state.bufferSize) {
      state.buffer.shift();
    }
  }
}

/** 仅测试使用：恢复默认配置并清空缓冲。 */
export function resetLogger(): void {
  state.level = DEFAULT_LEVEL;
  state.sink = null;
  state.bufferSize = DEFAULT_BUFFER_SIZE;
  state.buffer = [];
  state.emitting = false;
}

export function getBufferedLogs(): readonly LogRecord[] {
  return [...state.buffer];
}

export const logger = {
  debug(fields: LogFields): void {
    write('debug', fields);
  },
  info(fields: LogFields): void {
    write('info', fields);
  },
  warn(fields: LogFields): void {
    write('warn', fields);
  },
  error(fields: LogFields): void {
    write('error', fields);
  },
};

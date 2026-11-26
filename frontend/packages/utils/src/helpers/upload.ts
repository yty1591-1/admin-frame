/**
 * 上传文件类型校验与下载（FE-SEC-06）。
 *
 * 只做**展示前的前置拦截**：真正的类型/大小/内容校验必须由服务端完成。
 * 规则：扩展名与 MIME 双白名单；MIME 为空时只按扩展名判定（浏览器对部分
 * 类型不给 `type`）；SVG 默认禁止（可携带脚本），需要时显式 `allowSvg`。
 */

/** 默认允许的扩展名（小写、含点号）。 */
export const DEFAULT_UPLOAD_EXTENSIONS = [
  '.bmp',
  '.csv',
  '.doc',
  '.docx',
  '.gif',
  '.jpeg',
  '.jpg',
  '.pdf',
  '.png',
  '.txt',
  '.webp',
  '.xls',
  '.xlsx',
  '.zip',
] as const;

/** 默认允许的 MIME 类型。 */
export const DEFAULT_UPLOAD_MIME_TYPES = [
  'application/msword',
  'application/pdf',
  'application/vnd.ms-excel',
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'application/x-zip-compressed',
  'application/zip',
  'image/bmp',
  'image/gif',
  'image/jpeg',
  'image/png',
  'image/webp',
  'text/csv',
  'text/plain',
] as const;

/** 白名单扩展名（含 SVG，仅用于 allowSvg 分支）。 */
const SVG_EXTENSION = '.svg';
const SVG_MIME = 'image/svg+xml';

export interface UploadValidationOptions {
  /** 自定义允许扩展名，缺省 {@link DEFAULT_UPLOAD_EXTENSIONS}。 */
  extensions?: readonly string[];
  /** 自定义允许 MIME，缺省 {@link DEFAULT_UPLOAD_MIME_TYPES}。 */
  mimeTypes?: readonly string[];
  /** 是否允许 SVG（默认 false）。 */
  allowSvg?: boolean;
}

export interface UploadValidationResult {
  ok: boolean;
  /** 拒绝原因；ok 时为 undefined。 */
  reason?: 'extension' | 'mime' | 'svg';
  /** 解析出的扩展名（小写、含点号；无扩展名时为空串）。 */
  extension: string;
  /** 文件声明的 MIME（小写；可能为空串）。 */
  mime: string;
}

/** 取小写扩展名（含点号）；无扩展名返回空串。 */
export function getFileExtension(name?: string): string {
  if (!name) {
    return '';
  }
  const index = name.lastIndexOf('.');
  if (index === -1) {
    return '';
  }
  return name.slice(index).toLowerCase();
}

/**
 * 校验上传文件是否落在扩展名与 MIME 双白名单内。
 *
 * - 扩展名不在白名单 → `reason: 'extension'`；
 * - MIME 非空且不在白名单 → `reason: 'mime'`（防 `evil.png` 实为 HTML）；
 * - SVG 且未放行 → `reason: 'svg'`。
 */
export function validateUploadFile(
  file: { name?: string; type?: string },
  options: UploadValidationOptions = {},
): UploadValidationResult {
  const extension = getFileExtension(file.name);
  const mime = (file.type || '').toLowerCase();

  if ((extension === SVG_EXTENSION || mime === SVG_MIME) && !options.allowSvg) {
    return { extension, mime, ok: false, reason: 'svg' };
  }

  const extensions =
    options.extensions ??
    (options.allowSvg
      ? [...DEFAULT_UPLOAD_EXTENSIONS, SVG_EXTENSION]
      : DEFAULT_UPLOAD_EXTENSIONS);
  if (!extension || !extensions.includes(extension)) {
    return { extension, mime, ok: false, reason: 'extension' };
  }

  const mimeTypes =
    options.mimeTypes ??
    (options.allowSvg
      ? [...DEFAULT_UPLOAD_MIME_TYPES, SVG_MIME]
      : DEFAULT_UPLOAD_MIME_TYPES);
  if (mime && !mimeTypes.includes(mime)) {
    return { extension, mime, ok: false, reason: 'mime' };
  }

  return { extension, mime, ok: true };
}

/**
 * 触发浏览器下载（非图片预览走这里，避免内联打开执行同源 HTML）。
 *
 * 跨源 URL 会忽略 `download`，此时退化为新窗口打开并切断 `opener`。
 */
export function downloadFile(url: string, filename?: string): void {
  if (typeof document === 'undefined' || !url) {
    return;
  }
  const anchor = document.createElement('a');
  anchor.href = url;
  if (filename) {
    anchor.download = filename;
  }
  anchor.rel = 'noopener noreferrer';
  anchor.target = '_blank';
  anchor.style.display = 'none';
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
}

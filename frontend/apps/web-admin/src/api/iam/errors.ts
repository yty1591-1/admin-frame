/** IAM 写操作的错误语义；避免页面各自解析 Axios 结构。 */

interface ApiErrorLike {
  message?: string;
  response?: {
    data?: { code?: number; message?: string };
    status?: number;
  };
}

function statusOf(error: unknown): number | undefined {
  return (error as ApiErrorLike)?.response?.status;
}

/** 409：可能是版本冲突、code 重复或审批拦截。 */
export function isConflict(error: unknown): boolean {
  return statusOf(error) === 409;
}

/** 404：资源不存在或越权对外表现。 */
export function isNotFound(error: unknown): boolean {
  return statusOf(error) === 404;
}

/** 403：无权限或功能开关关闭（如破玻璃）。 */
export function isForbidden(error: unknown): boolean {
  return statusOf(error) === 403;
}

/**
 * 提取后端 message，仅供冲突弹窗等需要原文案的场景。
 * 通用错误 toast 由全局 `errorMessageResponseInterceptor`（FE-SEC-03）负责，页面勿再 `message.error`。
 */
export function getErrorMessage(error: unknown, fallback = '操作失败'): string {
  const message = (error as ApiErrorLike)?.response?.data?.message;
  if (message) {
    return message;
  }
  return (error as ApiErrorLike)?.message || fallback;
}

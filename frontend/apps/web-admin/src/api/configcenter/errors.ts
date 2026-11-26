/** 配置中心写操作的错误语义；避免页面各自解析 Axios 结构。 */

interface ApiErrorLike {
  message?: string;
  response?: {
    data?: { code?: number; message?: string };
    status?: number;
  };
}

/**
 * 提取后端 message，仅供需要原文案的局部展示。
 * 通用错误 toast 由全局拦截器（FE-SEC-03）负责，页面勿再 `message.error`。
 */
export function getConfigErrorMessage(
  error: unknown,
  fallback = '操作失败',
): string {
  const message = (error as ApiErrorLike)?.response?.data?.message;
  if (message) {
    return message;
  }
  return (error as ApiErrorLike)?.message || fallback;
}

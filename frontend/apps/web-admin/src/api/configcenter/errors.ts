/** 配置中心写操作的错误语义；避免页面各自解析 Axios 结构。 */

interface ApiErrorLike {
  message?: string;
  response?: {
    data?: { code?: number; message?: string };
    status?: number;
  };
}

/** 提取后端 message，缺失时返回兜底文案。 */
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

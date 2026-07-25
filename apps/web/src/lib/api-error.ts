import type { ApiErrorBody, ErrorCode } from '@/types';

/** Map auth/edge error codes to Chinese user-facing messages. */
const CODE_MESSAGES: Record<ErrorCode, string> = {
  invalid_credentials: '邮箱或密码错误',
  email_taken: '该邮箱已被注册',
  invalid_token: '登录已失效，请重新登录',
  invalid_refresh_token: '登录已失效，请重新登录',
  password_too_weak: '密码强度不足，请使用更复杂的密码',
  invalid_email: '邮箱格式不正确',
  bad_request: '请求参数有误',
  unauthorized: '未授权，请登录',
  internal_error: '服务暂时不可用，请稍后重试',
  // Edge/BFF
  billing_unavailable: '套餐服务暂时不可用，请稍后重试',
  logging_unavailable: '日志服务暂时不可用，请稍后重试',
  auth_unavailable: '认证服务暂时不可用，请稍后重试',
  auth_error: '认证服务异常，请稍后重试',
  quota_unavailable: '配额服务暂时不可用，请稍后重试',
  not_found: '未找到相关资源',
  missing_request_id: '缺少请求 ID',
  invalid_json: '请求数据格式不正确',
  invalid_preferred_billing: '计费偏好设置无效',
};

export class ApiError extends Error {
  code: ErrorCode;
  status: number;

  constructor(code: ErrorCode, message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }
}

export function parseApiError(res: Response, body: unknown): ApiError {
  const rawErr = (body as ApiErrorBody | null)?.error;
  // Simplified wire shape: {error: "code_string"} (panel handlers).
  if (typeof rawErr === 'string') {
    const code = rawErr as ErrorCode;
    return new ApiError(code, CODE_MESSAGES[code] ?? '请求失败', res.status);
  }
  // Contract shape: {error: {code, message}} (keys handler / Auth).
  if (rawErr && typeof rawErr === 'object') {
    return new ApiError(rawErr.code, CODE_MESSAGES[rawErr.code] ?? rawErr.message, res.status);
  }
  // Fallback by status.
  if (res.status === 401)
    return new ApiError('unauthorized', CODE_MESSAGES.unauthorized, 401);
  if (res.status === 400)
    return new ApiError('bad_request', CODE_MESSAGES.bad_request, 400);
  if (res.status === 404)
    return new ApiError('not_found', CODE_MESSAGES.not_found, 404);
  if (res.status >= 500)
    return new ApiError('internal_error', CODE_MESSAGES.internal_error, res.status);
  return new ApiError('bad_request', '请求失败', res.status);
}

export function networkError(): ApiError {
  return new ApiError('internal_error', '网络连接失败，请检查网络', 0);
}

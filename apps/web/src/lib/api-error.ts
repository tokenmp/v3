import type { ApiErrorBody, ErrorCode } from '@/types';

/** Map auth error codes to Chinese user-facing messages. */
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
  const err = (body as ApiErrorBody | null)?.error;
  if (err) {
    return new ApiError(err.code, CODE_MESSAGES[err.code] ?? err.message, res.status);
  }
  // Fallback by status.
  if (res.status === 401)
    return new ApiError('unauthorized', CODE_MESSAGES.unauthorized, 401);
  if (res.status === 400)
    return new ApiError('bad_request', CODE_MESSAGES.bad_request, 400);
  if (res.status >= 500)
    return new ApiError('internal_error', CODE_MESSAGES.internal_error, res.status);
  const fallbackMsg = (body as ApiErrorBody | null)?.error?.message ?? '请求失败';
  return new ApiError('bad_request', fallbackMsg, res.status);
}

export function networkError(): ApiError {
  return new ApiError('internal_error', '网络连接失败，请检查网络', 0);
}

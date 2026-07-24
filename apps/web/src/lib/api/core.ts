import { useAuthStore } from '@/lib/auth';
import { networkError, parseApiError } from '@/lib/api-error';
import type { ApiErrorBody, TokenResponse } from '@/types';

/** Base URL of the auth service. Empty string => relative to current origin (same-origin proxy). */
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? '';

/** Base URL of the notice service. Empty string => relative to current origin (same-origin proxy). */
export const NOTICE_BASE = process.env.NEXT_PUBLIC_NOTICE_API_BASE ?? '';

let refreshing: Promise<boolean> | null = null;

async function refreshTokens(): Promise<boolean> {
  const { refreshToken, setTokens, logout } = useAuthStore.getState();
  if (!refreshToken) return false;

  if (refreshing) return refreshing;

  refreshing = (async () => {
    try {
      const res = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as ApiErrorBody | null;
        throw parseApiError(res, body);
      }
      const tokens = (await res.json()) as TokenResponse;
      setTokens(tokens);
      return true;
    } catch {
      logout();
      if (typeof window !== 'undefined') {
        window.dispatchEvent(new CustomEvent('tokenmp:auth-expired'));
      }
      return false;
    } finally {
      refreshing = null;
    }
  })();

  return refreshing;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Skip auth header + auto-refresh (for login/register/refresh). */
  auth?: boolean;
  /** Expect 204 no-content. */
  noContent?: boolean;
  /** Override the API base (default API_BASE). Used by the notice service calls. */
  baseUrl?: string;
  signal?: AbortSignal;
}

export async function request<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const { method = 'GET', body, auth = true, noContent = false, baseUrl, signal } = opts;
  const { accessToken } = useAuthStore.getState();
  const base = baseUrl ?? API_BASE;

  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (auth && accessToken) headers.Authorization = `Bearer ${accessToken}`;

  const doFetch = (token: string | null): Promise<Response> =>
    fetch(`${base}${path}`, {
      method,
      headers: {
        ...headers,
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal,
    });

  let res = await doFetch(accessToken).catch(() => {
    throw networkError();
  });

  if (res.status === 401 && auth) {
    const ok = await refreshTokens();
    if (ok) {
      res = await doFetch(useAuthStore.getState().accessToken).catch(() => {
        throw networkError();
      });
    }
  }

  if (!res.ok) {
    const errBody = (await res.json().catch(() => null)) as ApiErrorBody | null;
    throw parseApiError(res, errBody);
  }

  if (noContent || res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

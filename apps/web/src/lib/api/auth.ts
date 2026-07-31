import { parseApiError } from '@/lib/api-error';
import { request } from '@/lib/api/core';
import type { AccessTokenResponse, User } from '@/types';

export interface RegisterInput {
  email: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

export interface ChangePasswordInput {
  current_password: string;
  new_password: string;
}

export const authApi = {
  register: (input: RegisterInput) =>
    request<User>('/api/v1/auth/register', {
      method: 'POST',
      body: input,
      auth: false,
    }),

  login: async (input: LoginInput): Promise<AccessTokenResponse> => {
    const res = await fetch('/api/auth/session/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
      credentials: 'same-origin',
    });
    if (!res.ok) {
      const body = await res.json().catch(() => null);
      throw parseApiError(res, body);
    }
    return res.json() as Promise<AccessTokenResponse>;
  },

  logout: () =>
    request<void>('/api/auth/session/logout', {
      method: 'POST',
      auth: false,
      noContent: true,
      baseUrl: '',
    }),

  logoutAll: async () => {
    await request<void>('/api/v1/auth/logout-all', { method: 'POST', noContent: true });
    await authApi.logout();
  },

  me: () => request<User>('/api/v1/auth/me'),

  changePassword: (input: ChangePasswordInput) =>
    request<void>('/api/v1/auth/password', {
      method: 'PUT',
      body: input,
      noContent: true,
    }),
};

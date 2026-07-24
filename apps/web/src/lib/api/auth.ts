import { request } from '@/lib/api/core';
import { mockAuthApi } from '@/lib/api/mock-auth';
import type { TokenResponse, User } from '@/types';

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

/** Use mock auth when no real backend is reachable (default for local dev).
 *  Set NEXT_PUBLIC_USE_MOCK_AUTH=0 to use the real fetch client. */
const USE_MOCK = process.env.NEXT_PUBLIC_USE_MOCK_AUTH !== '0';

const realAuthApi = {
  register: (input: RegisterInput) =>
    request<TokenResponse>('/api/v1/auth/register', {
      method: 'POST',
      body: input,
      auth: false,
    }),

  login: (input: LoginInput) =>
    request<TokenResponse>('/api/v1/auth/login', {
      method: 'POST',
      body: input,
      auth: false,
    }),

  logout: (refreshToken: string) =>
    request<void>('/api/v1/auth/logout', {
      method: 'POST',
      body: { refresh_token: refreshToken },
      noContent: true,
    }),

  logoutAll: () =>
    request<void>('/api/v1/auth/logout-all', { method: 'POST', noContent: true }),

  me: () => request<User>('/api/v1/auth/me'),

  changePassword: (input: ChangePasswordInput) =>
    request<void>('/api/v1/auth/password', {
      method: 'PUT',
      body: input,
      noContent: true,
    }),
};

/** Unified auth API surface. Mock-backed by default. */
export const authApi = USE_MOCK
  ? {
      login: mockAuthApi.login,
      register: mockAuthApi.register,
      me: mockAuthApi.me,
      logout: mockAuthApi.logout,
      logoutAll: mockAuthApi.logoutAll,
      changePassword: mockAuthApi.changePassword,
      /** Mock-only helper to resolve the user after login. */
      getUserByEmail: mockAuthApi.getUserByEmail,
    }
  : realAuthApi;

import { request } from '@/lib/api/core';
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

export const authApi = {
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

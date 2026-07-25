/** Mock auth API. Returns the same shapes as the real auth service,
 *  but resolves from an in-memory user list with simulated latency.
 *  Swap to `./auth.ts` (the real fetch client) by setting
 *  NEXT_PUBLIC_USE_MOCK_AUTH=0 once the auth backend is reachable. */

import type { TokenResponse, User } from '@/types';

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

const MOCK_USERS: Array<User & { password: string }> = [
  {
    id: 'user_0001',
    email: 'demo@tokenmp.cn',
    password: 'demo1234',
    role: 'user',
    status: 'active',
    created_at: '2026-01-01T00:00:00Z',
  },
  {
    id: 'user_0002',
    email: 'admin@tokenmp.cn',
    password: 'admin1234',
    role: 'admin',
    status: 'active',
    created_at: '2025-12-01T00:00:00Z',
  },
];

class MockAuthError extends Error {
  code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

/** Tracks the most recently logged-in email so `me()` can resolve the user. */
let currentEmail: string | null = null;

function makeTokens(): TokenResponse {
  return {
    access_token: 'mock-access-' + Math.random().toString(36).slice(2),
    refresh_token: 'mock-refresh-' + Math.random().toString(36).slice(2),
    token_type: 'Bearer',
    expires_in: 900,
  };
}

function publicUser(u: (typeof MOCK_USERS)[number]): User {
  const { password: _password, ...rest } = u;
  return rest;
}

export const mockAuthApi = {
  login: async (input: { email: string; password: string }) => {
    await delay(400);
    const user = MOCK_USERS.find(
      (u) => u.email.toLowerCase() === input.email.toLowerCase().trim(),
    );
    if (!user || user.password !== input.password) {
      throw new MockAuthError('invalid_credentials', '邮箱或密码错误');
    }
    currentEmail = user.email;
    return makeTokens();
  },

  register: async (input: { email: string; password: string }) => {
    await delay(500);
    const exists = MOCK_USERS.some(
      (u) => u.email.toLowerCase() === input.email.toLowerCase().trim(),
    );
    if (exists) {
      throw new MockAuthError('email_taken', '该邮箱已被注册');
    }
    const newUser = {
      id: 'user_' + Math.random().toString(36).slice(2, 10),
      email: input.email.toLowerCase().trim(),
      password: input.password,
      role: 'user' as const,
      status: 'active' as const,
      created_at: new Date().toISOString(),
    };
    MOCK_USERS.push(newUser);
    currentEmail = newUser.email;
    return makeTokens();
  },

  me: async () => {
    await delay(200);
    const email = currentEmail ?? MOCK_USERS[0]!.email;
    const user = MOCK_USERS.find(
      (u) => u.email.toLowerCase() === email.toLowerCase(),
    );
    if (!user) throw new MockAuthError('invalid_credentials', '用户不存在');
    return publicUser(user);
  },

  logout: async (_refreshToken: string) => {
    await delay(200);
  },

  logoutAll: async () => {
    await delay(200);
  },

  changePassword: async (input: {
    current_password: string;
    new_password: string;
  }) => {
    await delay(400);
    if (input.current_password !== 'demo1234') {
      throw new MockAuthError('invalid_credentials', '当前密码错误');
    }
    if (input.new_password.length < 12) {
      throw new MockAuthError('password_too_weak', '密码强度不足');
    }
  },

  /** Helper: get the mock user matching an email (used after login to fetch the user). */
  getUserByEmail: async (email: string) => {
    await delay(150);
    const user = MOCK_USERS.find(
      (u) => u.email.toLowerCase() === email.toLowerCase().trim(),
    );
    if (!user) throw new MockAuthError('invalid_credentials', '用户不存在');
    return publicUser(user);
  },
};

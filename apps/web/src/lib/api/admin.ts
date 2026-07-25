/** Admin API: dashboard stats, users, api-keys, request-logs.
 *  Mock-backed by default (NEXT_PUBLIC_USE_MOCK_ADMIN defaults on); set
 *  NEXT_PUBLIC_USE_MOCK_ADMIN=0 to call the real Edge admin endpoints via
 *  BIZ_BASE. The Edge requires role=admin JWT claims. Components are agnostic
 *  to the source. */

import type {
  AdminApiKey,
  AdminDashboardStats,
  AdminRequestLog,
  AdminRequestLogListResponse,
  AdminUser,
  AdminUserDetail,
  AdminUserListResponse,
} from '@/types/admin';
import { request, API_BASE } from './core';

const useMock = process.env.NEXT_PUBLIC_USE_MOCK_ADMIN !== '0';
const ADMIN_BASE = process.env.NEXT_PUBLIC_BIZ_API_BASE ?? API_BASE;

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const mockUsers: AdminUser[] = [
  { id: 'u_1', email: 'demo@tokenmp.cn', role: 'user', status: 'active', createdAt: '2026-07-20T03:14:00Z' },
  { id: 'u_2', email: 'admin@tokenmp.cn', role: 'admin', status: 'active', createdAt: '2026-07-19T01:02:00Z' },
  { id: 'u_3', email: 'alice@example.com', role: 'user', status: 'active', createdAt: '2026-07-22T08:30:00Z' },
  { id: 'u_4', email: 'bob@example.com', role: 'user', status: 'disabled', createdAt: '2026-07-18T14:20:00Z' },
  { id: 'u_5', email: 'carol@example.com', role: 'user', status: 'active', createdAt: '2026-07-23T09:15:00Z' },
];

const mockModels = ['gpt-4o', 'claude-3-5-sonnet', 'gpt-4o-mini', 'deepseek-chat', 'glm-4.5'];

function genMockLogs(count: number): AdminRequestLog[] {
  const out: AdminRequestLog[] = [];
  const now = Date.now();
  for (let i = 0; i < count; i++) {
    const ok = i % 7 !== 0;
    const userId = mockUsers[i % mockUsers.length]!.id;
    out.push({
      requestId: `req_${(now - i * 1000).toString(36)}_${i}`,
      userId,
      userEmail: mockUsers[i % mockUsers.length]!.email,
      model: mockModels[i % mockModels.length]!,
      status: ok ? 'success' : 'error',
      inputTokens: 50 + ((i * 31) % 400),
      outputTokens: 80 + ((i * 53) % 600),
      cost: (0.001 * (i + 1)).toFixed(4),
      durationMs: 400 + ((i * 137) % 1800),
      createdAt: new Date(now - i * 1000 * 60 * 3).toISOString(),
    });
  }
  return out;
}

const allMockLogs = genMockLogs(63);

function genTrend(): AdminDashboardStats['trend'] {
  const trend: AdminDashboardStats['trend'] = [];
  const today = new Date();
  for (let i = 14; i >= 0; i--) {
    const d = new Date(today);
    d.setDate(d.getDate() - i);
    const req = 80 + Math.floor(Math.random() * 120);
    const succ = Math.floor(req * (0.85 + Math.random() * 0.12));
    trend.push({
      date: d.toISOString().slice(0, 10),
      requests: req,
      success: succ,
      inputTokens: req * 200 + Math.floor(Math.random() * 1000),
      outputTokens: req * 350 + Math.floor(Math.random() * 1500),
    });
  }
  return trend;
}

function genDashboardStats(): AdminDashboardStats {
  const trend = genTrend();
  const today = trend[trend.length - 1]!;
  return {
    totalUsers: mockUsers.length,
    totalRequests: allMockLogs.length,
    todayRequests: today.requests,
    todaySuccess: today.success,
    todayActiveUsers: 3,
    todayTokens: today.inputTokens + today.outputTokens,
    successRate: today.requests > 0 ? Math.round((today.success / today.requests) * 1000) / 10 : 0,
    trend,
    todayModelUsage: mockModels.slice(0, 3).map((m, i) => ({
      model: m!,
      requests: 10 + i * 3,
      success: 8 + i * 3,
      tokens: 3000 + i * 1500,
    })),
    todayTopUsers: mockUsers.slice(0, 3).map((u, i) => ({
      email: u.email,
      requests: 15 - i * 4,
      tokens: 5000 - i * 1200,
      cost: (0.8 - i * 0.2).toFixed(4),
    })),
  };
}

// ---------------------------------------------------------------------------
// Real API helpers
// ---------------------------------------------------------------------------

async function realDashboard(days = 15): Promise<AdminDashboardStats> {
  return request<AdminDashboardStats>(`/api/v1/admin/stats?days=${days}`, { baseUrl: ADMIN_BASE });
}

async function realListUsers(
  page = 1, pageSize = 20, search = '',
): Promise<AdminUserListResponse> {
  const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  if (search) qs.set('search', search);
  return request<AdminUserListResponse>(`/api/v1/admin/users?${qs}`, { baseUrl: ADMIN_BASE });
}

async function realGetUser(id: string): Promise<AdminUserDetail> {
  return request<AdminUserDetail>(`/api/v1/admin/users/${id}`, { baseUrl: ADMIN_BASE });
}

async function realUpdateUser(id: string, input: { status?: string; role?: string }): Promise<AdminUser> {
  const r = await request<{ user: AdminUser }>(`/api/v1/admin/users/${id}`, {
    method: 'PATCH', body: input, baseUrl: ADMIN_BASE,
  });
  return r.user;
}

async function realListKeys(page = 1, pageSize = 20): Promise<{ keys: AdminApiKey[]; total: number }> {
  const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  const r = await request<{ keys: AdminApiKey[]; total: number }>(`/api/v1/admin/keys?${qs}`, { baseUrl: ADMIN_BASE });
  return { keys: r.keys, total: r.total };
}

async function realListLogs(
  page = 1, pageSize = 20,
): Promise<AdminRequestLogListResponse> {
  const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  return request<AdminRequestLogListResponse>(`/api/v1/admin/request-logs?${qs}`, { baseUrl: ADMIN_BASE });
}

// ---------------------------------------------------------------------------
// Unified surface
// ---------------------------------------------------------------------------

export const adminApi = {
  // ---- Dashboard ----
  getDashboardStats: async (): Promise<AdminDashboardStats> => {
    if (useMock) { await delay(300); return genDashboardStats(); }
    return realDashboard();
  },

  // ---- Users ----
  listUsers: async (page = 1, pageSize = 20, search = ''): Promise<AdminUserListResponse> => {
    if (useMock) {
      await delay(280);
      let users = [...mockUsers];
      if (search) {
        const kw = search.toLowerCase();
        users = users.filter((u) => u.email.toLowerCase().includes(kw));
      }
      const total = users.length;
      const start = (page - 1) * pageSize;
      return { users: users.slice(start, start + pageSize), total, page, pageSize };
    }
    return realListUsers(page, pageSize, search);
  },
  getUser: async (id: string): Promise<AdminUserDetail> => {
    if (useMock) {
      await delay(300);
      const u = mockUsers.find((x) => x.id === id);
      if (!u) throw new Error('not_found');
      const recent = allMockLogs.filter((l) => l.userId === id).slice(0, 5);
      return {
        ...u,
        apiKeys: [
          { id: 'k_1', name: '默认密钥', keyPrefix: 'tmp_a1b2', keySuffix: 'c3d4', status: 'active', lastUsedAt: '2026-07-24T01:22:00Z', expiresAt: null, createdAt: '2026-07-20T03:14:00Z' },
        ],
        userPlans: [
          { id: 'up_1', planId: 'plan_starter', planType: 'token', totalQuota: '500000', remainingQuota: '371600', priority: 1, status: 'active', activatedAt: '2026-07-20T03:14:00Z', expiresAt: '2026-08-24T00:00:00Z' },
        ],
        recentRequests: recent,
        totalRequests: allMockLogs.filter((l) => l.userId === id).length,
      };
    }
    return realGetUser(id);
  },
  updateUser: async (id: string, input: { status?: 'active' | 'disabled'; role?: 'user' | 'admin' }): Promise<AdminUser> => {
    if (useMock) {
      await delay(250);
      const u = mockUsers.find((x) => x.id === id);
      if (!u) throw new Error('not_found');
      if (input.status !== undefined) u.status = input.status;
      if (input.role !== undefined) u.role = input.role;
      return u;
    }
    return realUpdateUser(id, input);
  },

  // ---- API Keys (global) ----
  listKeys: async (page = 1, pageSize = 20): Promise<{ keys: AdminApiKey[]; total: number }> => {
    if (useMock) {
      await delay(280);
      const keys: AdminApiKey[] = mockUsers.flatMap((u) => [
        { id: `k_${u.id}`, userEmail: u.email, name: '默认密钥', keyPrefix: 'tmp_' + u.id.slice(-4), keySuffix: u.id.slice(0, 4), status: u.status === 'active' ? 'active' : 'disabled', lastUsedAt: '2026-07-24T01:22:00Z', expiresAt: null, createdAt: u.createdAt },
      ]);
      const total = keys.length;
      const start = (page - 1) * pageSize;
      return { keys: keys.slice(start, start + pageSize), total };
    }
    return realListKeys(page, pageSize);
  },

  // ---- Request logs (global) ----
  listRequestLogs: async (page = 1, pageSize = 20): Promise<AdminRequestLogListResponse> => {
    if (useMock) {
      await delay(300);
      const total = allMockLogs.length;
      const start = (page - 1) * pageSize;
      return { logs: allMockLogs.slice(start, start + pageSize), total, page, pageSize };
    }
    return realListLogs(page, pageSize);
  },
  getRequestLog: async (id: string): Promise<AdminRequestLog> => {
    if (useMock) {
      await delay(200);
      return allMockLogs.find((l) => l.requestId === id) ?? allMockLogs[0]!;
    }
    return request<AdminRequestLog>(`/api/v1/admin/request-logs/${id}`, { baseUrl: ADMIN_BASE });
  },
};

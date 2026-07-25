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

// ---------------------------------------------------------------------------
// Content management: announcements, changelogs, notifications
// (Phase 2 — mock-backed; backend admin write endpoints TBD)
// ---------------------------------------------------------------------------

import type {
  AdminAnnouncement,
  AdminAnnouncementInput,
  AdminChangelog,
  AdminChangelogInput,
  AdminNotification,
  AdminNotificationInput,
} from '@/types/admin';

const mockAnnouncements: AdminAnnouncement[] = [
  { id: 'a_1', title: '系统维护通知', summary: '将于凌晨进行系统维护', body: '## 维护详情\n\n将于 **2026-07-30 凌晨 2:00-4:00** 进行系统维护，期间服务可能短暂中断。', severity: 'warning', publishedAt: '2026-07-24T10:00:00Z', createdAt: '2026-07-24T09:00:00Z', updatedAt: '2026-07-24T10:00:00Z' },
  { id: 'a_2', title: '新模型上线', summary: 'GPT-4o 已上线', body: '## 新模型\n\n**GPT-4o** 现已可用，支持 vision 和 tools。', severity: 'success', publishedAt: '2026-07-22T08:00:00Z', createdAt: '2026-07-22T07:00:00Z', updatedAt: '2026-07-22T08:00:00Z' },
  { id: 'a_3', title: '草稿公告', summary: '尚未发布', body: '## 草稿\n\n这是草稿内容。', severity: 'info', publishedAt: null, createdAt: '2026-07-23T15:00:00Z', updatedAt: '2026-07-23T15:00:00Z' },
];

const mockChangelogs: AdminChangelog[] = [
  { id: 'c_1', version: 'v3.1.0', title: '后台管理上线', body: '## 新功能\n\n- Admin 后台管理\n- 用户管理\n- 请求日志查看', publishedAt: '2026-07-25T00:00:00Z', createdAt: '2026-07-25T00:00:00Z', updatedAt: '2026-07-25T00:00:00Z' },
  { id: 'c_2', version: 'v3.0.0', title: 'v3 正式发布', body: '## 重大更新\n\n全新架构，Edge/BFF + 微服务。', publishedAt: '2026-07-20T00:00:00Z', createdAt: '2026-07-20T00:00:00Z', updatedAt: '2026-07-20T00:00:00Z' },
];

const mockNotifications: AdminNotification[] = [
  { id: 'n_1', userId: 'u_1', type: 'info', title: '套餐即将到期', body: '您的入门套餐将于 7 天后到期。', action: { type: 'link', label: '查看套餐', href: '/panel/billing/plans' }, readAt: null, createdAt: '2026-07-24T03:00:00Z' },
  { id: 'n_2', userId: 'u_2', type: 'success', title: '密钥创建成功', body: '您的新 API 密钥已创建。', action: null, readAt: '2026-07-23T10:00:00Z', createdAt: '2026-07-23T09:00:00Z' },
];

function genId(prefix: string) {
  return prefix + '_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
}

// Announcements
export const adminAnnouncementApi = {
  list: async (): Promise<AdminAnnouncement[]> => {
    if (useMock) { await delay(260); return [...mockAnnouncements].sort((a, b) => (b.publishedAt ?? '').localeCompare(a.publishedAt ?? '')); }
    return request<AdminAnnouncement[]>('/api/v1/admin/announcements', { baseUrl: ADMIN_BASE });
  },
  create: async (input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
    if (useMock) {
      await delay(350);
      const now = new Date().toISOString();
      const item: AdminAnnouncement = { ...input, id: genId('a'), createdAt: now, updatedAt: now };
      mockAnnouncements.unshift(item);
      return item;
    }
    const r = await request<{ announcement: AdminAnnouncement }>('/api/v1/admin/announcements', { method: 'POST', body: input, baseUrl: ADMIN_BASE });
    return r.announcement;
  },
  update: async (id: string, input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
    if (useMock) {
      await delay(300);
      const a = mockAnnouncements.find((x) => x.id === id);
      if (!a) throw new Error('not_found');
      Object.assign(a, input, { updatedAt: new Date().toISOString() });
      return a;
    }
    const r = await request<{ announcement: AdminAnnouncement }>(`/api/v1/admin/announcements/${id}`, { method: 'PATCH', body: input, baseUrl: ADMIN_BASE });
    return r.announcement;
  },
  delete: async (id: string): Promise<void> => {
    if (useMock) { await delay(300); const i = mockAnnouncements.findIndex((x) => x.id === id); if (i >= 0) mockAnnouncements.splice(i, 1); return; }
    await request<void>(`/api/v1/admin/announcements/${id}`, { method: 'DELETE', noContent: true, baseUrl: ADMIN_BASE });
  },
};

// Changelogs
export const adminChangelogApi = {
  list: async (): Promise<AdminChangelog[]> => {
    if (useMock) { await delay(260); return [...mockChangelogs].sort((a, b) => (b.publishedAt ?? '').localeCompare(a.publishedAt ?? '')); }
    return request<AdminChangelog[]>('/api/v1/admin/changelogs', { baseUrl: ADMIN_BASE });
  },
  create: async (input: AdminChangelogInput): Promise<AdminChangelog> => {
    if (useMock) {
      await delay(350);
      const now = new Date().toISOString();
      const item: AdminChangelog = { ...input, id: genId('c'), createdAt: now, updatedAt: now };
      mockChangelogs.unshift(item);
      return item;
    }
    const r = await request<{ changelog: AdminChangelog }>('/api/v1/admin/changelogs', { method: 'POST', body: input, baseUrl: ADMIN_BASE });
    return r.changelog;
  },
  update: async (id: string, input: AdminChangelogInput): Promise<AdminChangelog> => {
    if (useMock) {
      await delay(300);
      const c = mockChangelogs.find((x) => x.id === id);
      if (!c) throw new Error('not_found');
      Object.assign(c, input, { updatedAt: new Date().toISOString() });
      return c;
    }
    const r = await request<{ changelog: AdminChangelog }>(`/api/v1/admin/changelogs/${id}`, { method: 'PATCH', body: input, baseUrl: ADMIN_BASE });
    return r.changelog;
  },
  delete: async (id: string): Promise<void> => {
    if (useMock) { await delay(300); const i = mockChangelogs.findIndex((x) => x.id === id); if (i >= 0) mockChangelogs.splice(i, 1); return; }
    await request<void>(`/api/v1/admin/changelogs/${id}`, { method: 'DELETE', noContent: true, baseUrl: ADMIN_BASE });
  },
};

// Notifications
export const adminNotificationApi = {
  list: async (): Promise<AdminNotification[]> => {
    if (useMock) { await delay(260); return [...mockNotifications].sort((a, b) => b.createdAt.localeCompare(a.createdAt)); }
    return request<AdminNotification[]>('/api/v1/admin/notifications', { baseUrl: ADMIN_BASE });
  },
  send: async (input: AdminNotificationInput): Promise<AdminNotification> => {
    if (useMock) {
      await delay(400);
      const item: AdminNotification = {
        ...input,
        id: genId('n'),
        userId: input.userId || 'all',
        readAt: null,
        createdAt: new Date().toISOString(),
      };
      mockNotifications.unshift(item);
      return item;
    }
    const r = await request<{ notification: AdminNotification }>('/api/v1/admin/notifications/send', { method: 'POST', body: input, baseUrl: ADMIN_BASE });
    return r.notification;
  },
  delete: async (id: string): Promise<void> => {
    if (useMock) { await delay(300); const i = mockNotifications.findIndex((x) => x.id === id); if (i >= 0) mockNotifications.splice(i, 1); return; }
    await request<void>(`/api/v1/admin/notifications/${id}`, { method: 'DELETE', noContent: true, baseUrl: ADMIN_BASE });
  },
};

// ---------------------------------------------------------------------------
// Plans + User plans + Usage (Phase 3 — mock-backed; backend admin endpoints TBD)
// ---------------------------------------------------------------------------

import type { AdminPlan, AdminPlanInput, AdminUserPlan, AdminUserPlanInput } from '@/types/admin';

const mockPlans: AdminPlan[] = [
  { id: 'p_1', name: '入门套餐', planType: 'token', price: 0, category: 'monthly', monthlyLimit: null, tokenLimit: 500000, allowedModels: ['gpt-4o-mini', 'deepseek-chat'], status: 'active', createdAt: '2026-07-20T00:00:00Z', updatedAt: '2026-07-20T00:00:00Z' },
  { id: 'p_2', name: '专业套餐', planType: 'token', price: 99, category: 'monthly', monthlyLimit: null, tokenLimit: 5000000, allowedModels: ['gpt-4o', 'claude-3-5-sonnet', 'gpt-4o-mini', 'deepseek-chat', 'glm-4.5'], status: 'active', createdAt: '2026-07-20T00:00:00Z', updatedAt: '2026-07-20T00:00:00Z' },
  { id: 'p_3', name: '编程套餐', planType: 'coding', price: 29, category: 'monthly', monthlyLimit: 1000, tokenLimit: null, allowedModels: ['gpt-4o-mini', 'deepseek-chat'], status: 'active', createdAt: '2026-07-20T00:00:00Z', updatedAt: '2026-07-20T00:00:00Z' },
  { id: 'p_4', name: '图像套餐', planType: 'image', price: 49, category: 'monthly', monthlyLimit: null, tokenLimit: null, allowedModels: ['dall-e-3'], status: 'disabled', createdAt: '2026-07-22T00:00:00Z', updatedAt: '2026-07-22T00:00:00Z' },
];

const mockUserPlans: AdminUserPlan[] = [
  { id: 'up_1', userId: 'u_1', userEmail: 'demo@tokenmp.cn', planId: 'p_1', planName: '入门套餐', planType: 'token', status: 'active', activatedAt: '2026-07-20T03:14:00Z', expiresAt: '2026-08-24T00:00:00Z', remainingQuota: '371600' },
  { id: 'up_2', userId: 'u_3', userEmail: 'alice@example.com', planId: 'p_2', planName: '专业套餐', planType: 'token', status: 'active', activatedAt: '2026-07-22T08:30:00Z', expiresAt: null, remainingQuota: '4890000' },
  { id: 'up_3', userId: 'u_4', userEmail: 'bob@example.com', planId: 'p_3', planName: '编程套餐', planType: 'coding', status: 'cancelled', activatedAt: '2026-07-18T14:20:00Z', expiresAt: '2026-07-20T14:20:00Z', remainingQuota: '0' },
];

const allModels = ['gpt-4o', 'gpt-4o-mini', 'claude-3-5-sonnet', 'deepseek-chat', 'glm-4.5', 'dall-e-3'];

// Plans
export const adminPlanApi = {
  list: async (): Promise<AdminPlan[]> => {
    if (useMock) { await delay(260); return [...mockPlans]; }
    return request<AdminPlan[]>('/api/v1/admin/plans', { baseUrl: ADMIN_BASE });
  },
  create: async (input: AdminPlanInput): Promise<AdminPlan> => {
    if (useMock) {
      await delay(350);
      const now = new Date().toISOString();
      const item: AdminPlan = { ...input, id: genId('p'), createdAt: now, updatedAt: now };
      mockPlans.unshift(item);
      return item;
    }
    const r = await request<{ plan: AdminPlan }>('/api/v1/admin/plans', { method: 'POST', body: input, baseUrl: ADMIN_BASE });
    return r.plan;
  },
  update: async (id: string, input: AdminPlanInput): Promise<AdminPlan> => {
    if (useMock) {
      await delay(300);
      const p = mockPlans.find((x) => x.id === id);
      if (!p) throw new Error('not_found');
      Object.assign(p, input, { updatedAt: new Date().toISOString() });
      return p;
    }
    const r = await request<{ plan: AdminPlan }>(`/api/v1/admin/plans/${id}`, { method: 'PATCH', body: input, baseUrl: ADMIN_BASE });
    return r.plan;
  },
  delete: async (id: string): Promise<void> => {
    if (useMock) { await delay(300); const i = mockPlans.findIndex((x) => x.id === id); if (i >= 0) mockPlans.splice(i, 1); return; }
    await request<void>(`/api/v1/admin/plans/${id}`, { method: 'DELETE', noContent: true, baseUrl: ADMIN_BASE });
  },
  listModels: async (): Promise<string[]> => {
    if (useMock) { await delay(100); return allModels; }
    return request<string[]>('/api/v1/admin/models/catalog', { baseUrl: ADMIN_BASE });
  },
};

// User plans
export const adminUserPlanApi = {
  list: async (): Promise<AdminUserPlan[]> => {
    if (useMock) { await delay(260); return [...mockUserPlans]; }
    return request<AdminUserPlan[]>('/api/v1/admin/user-plans', { baseUrl: ADMIN_BASE });
  },
  assign: async (input: AdminUserPlanInput): Promise<AdminUserPlan> => {
    if (useMock) {
      await delay(400);
      const plan = mockPlans.find((p) => p.id === input.planId);
      const user = mockUsers.find((u) => u.id === input.userId);
      if (!plan || !user) throw new Error('not_found');
      const item: AdminUserPlan = {
        id: genId('up'), userId: input.userId, userEmail: user.email,
        planId: plan.id, planName: plan.name, planType: plan.planType,
        status: 'active', activatedAt: new Date().toISOString(),
        expiresAt: input.expiresAt, remainingQuota: String(plan.tokenLimit ?? plan.monthlyLimit ?? 0),
      };
      mockUserPlans.unshift(item);
      return item;
    }
    const r = await request<{ userPlan: AdminUserPlan }>('/api/v1/admin/user-plans', { method: 'POST', body: input, baseUrl: ADMIN_BASE });
    return r.userPlan;
  },
  cancel: async (id: string): Promise<void> => {
    if (useMock) { await delay(300); const up = mockUserPlans.find((x) => x.id === id); if (up) up.status = 'cancelled'; return; }
    await request<void>(`/api/v1/admin/user-plans/${id}/cancel`, { method: 'POST', noContent: true, baseUrl: ADMIN_BASE });
  },
};

// ---------------------------------------------------------------------------
// Executor config: providers, models, routes (Phase 4 — read-only,
// mock-backed; write paths depend on Config Service draft/publish TBD)
// ---------------------------------------------------------------------------

import type { AdminProvider, AdminModelConfig, AdminRouteConfig } from '@/types/admin';

const mockProviders: AdminProvider[] = [
  { id: 'openai-default', name: 'OpenAI Default', displayLabel: 'a', selector: 'openai', baseURL: 'https://api.openai.example/v1', sdkKind: 'openai', protocol: 'openai_chat', status: 'active', credentialCount: 2, routeCount: 3 },
  { id: 'anthropic-default', name: 'Anthropic Default', displayLabel: 'b', selector: 'anthropic', baseURL: 'https://api.anthropic.example', sdkKind: 'anthropic', protocol: 'anthropic_messages', status: 'active', credentialCount: 1, routeCount: 2 },
  { id: 'deepseek-default', name: 'DeepSeek', displayLabel: 'c', selector: 'deepseek', baseURL: 'https://api.deepseek.example/v1', sdkKind: 'openai', protocol: 'openai_chat', status: 'active', credentialCount: 1, routeCount: 1 },
  { id: 'xfyun-default', name: '讯飞星火', displayLabel: 'd', selector: 'xfyun', baseURL: 'https://spark-api.example', sdkKind: 'openai', protocol: 'openai_chat', status: 'disabled', credentialCount: 0, routeCount: 0 },
];

const mockModelConfigs: AdminModelConfig[] = [
  { id: 'chat-default', displayName: 'Default Chat', capabilities: ['text', 'tools', 'vision', 'thinking'], thinkingSupported: true, routeCount: 3 },
  { id: 'chat-fast', displayName: 'Fast Chat', capabilities: ['text'], thinkingSupported: false, routeCount: 1 },
  { id: 'chat-reasoning', displayName: 'Reasoning Chat', capabilities: ['text', 'tools', 'thinking'], thinkingSupported: true, routeCount: 2 },
  { id: 'image-gen', displayName: 'Image Gen', capabilities: ['image'], thinkingSupported: false, routeCount: 1 },
];

const mockRouteConfigs: AdminRouteConfig[] = [
  { id: 'route-chat-default', modelId: 'chat-default', providerId: 'openai-default', upstreamModel: 'gpt-4o', protocol: 'openai_chat', priority: 100, enabled: true, quarantined: false },
  { id: 'route-chat-anthropic', modelId: 'chat-default', providerId: 'anthropic-default', upstreamModel: 'claude-3-5-sonnet', protocol: 'anthropic_messages', priority: 90, enabled: true, quarantined: false },
  { id: 'route-chat-deepseek', modelId: 'chat-default', providerId: 'deepseek-default', upstreamModel: 'deepseek-chat', protocol: 'openai_chat', priority: 80, enabled: true, quarantined: true },
  { id: 'route-chat-fast', modelId: 'chat-fast', providerId: 'openai-default', upstreamModel: 'gpt-4o-mini', protocol: 'openai_chat', priority: 100, enabled: true, quarantined: false },
  { id: 'route-image-gen', modelId: 'image-gen', providerId: 'openai-default', upstreamModel: 'dall-e-3', protocol: 'openai_images', priority: 100, enabled: false, quarantined: false },
];

export const adminConfigApi = {
  listProviders: async (): Promise<AdminProvider[]> => {
    if (useMock) { await delay(280); return [...mockProviders]; }
    return request<AdminProvider[]>('/api/v1/admin/providers', { baseUrl: ADMIN_BASE });
  },
  listModels: async (): Promise<AdminModelConfig[]> => {
    if (useMock) { await delay(280); return [...mockModelConfigs]; }
    return request<AdminModelConfig[]>('/api/v1/admin/models-config', { baseUrl: ADMIN_BASE });
  },
  listRoutes: async (): Promise<AdminRouteConfig[]> => {
    if (useMock) { await delay(280); return [...mockRouteConfigs]; }
    return request<AdminRouteConfig[]>('/api/v1/admin/routes-config', { baseUrl: ADMIN_BASE });
  },
};

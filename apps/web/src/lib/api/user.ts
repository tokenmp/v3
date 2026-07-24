/** User business API: API keys, request logs, plans, balance, usage, settings.
 *  Aligned to packages/contracts/openapi/api/v1.yaml. Mock-backed by default
 *  (NEXT_PUBLIC_USE_MOCK_BIZ defaults on); set NEXT_PUBLIC_USE_MOCK_BIZ=0 to
 *  call the real BFF (services/api Edge) via BIZ_BASE. Components are agnostic
 *  to the source — both return the same contract shape. */

import type {
  ApiKey,
  ApiKeyCreated,
  CreateKeyInput,
  Plan,
  RequestLog,
  UpdateKeyInput,
  UserBalance,
  UserPlan,
  UserSettings,
  UserSettingsUpdate,
  UsageStats,
} from '@/types';
import { request, API_BASE } from './core';

const useMock = process.env.NEXT_PUBLIC_USE_MOCK_BIZ !== '0';
const BIZ_BASE = process.env.NEXT_PUBLIC_BIZ_API_BASE ?? API_BASE;

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// Mock data (contract-shaped)
// ---------------------------------------------------------------------------

const mockKeys: ApiKey[] = [
  {
    id: 'key_1',
    name: '默认密钥',
    keyPrefix: 'tmp_a1b2',
    keySuffix: 'c3d4',
    status: 'active',
    lastUsedAt: '2026-07-24T01:22:00Z',
    expiresAt: null,
    createdAt: '2026-07-20T03:14:00Z',
  },
  {
    id: 'key_2',
    name: '测试环境',
    keyPrefix: 'tmp_e5f6',
    keySuffix: 'g7h8',
    status: 'active',
    lastUsedAt: '2026-07-22T18:40:00Z',
    expiresAt: null,
    createdAt: '2026-07-18T09:02:00Z',
  },
];

const mockModels = ['gpt-4o', 'claude-3-5-sonnet', 'gpt-4o-mini', 'deepseek-chat', 'glm-4.5'];

function genMockLogs(count: number): RequestLog[] {
  const out: RequestLog[] = [];
  const now = Date.now();
  for (let i = 0; i < count; i++) {
    const ok = i % 7 !== 0;
    out.push({
      requestId: `req_${(now - i * 1000).toString(36)}_${i}`,
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

const allMockLogs = genMockLogs(47);

const mockBalance: UserBalance = { codingRemaining: '371600', tokenRemaining: '500000' };

const mockPlans: Plan[] = [
  {
    id: 'plan_starter',
    name: '入门套餐',
    planType: 'token',
    price: 0,
    durationDays: 30,
    totalQuota: '500000',
    allowedModels: ['gpt-4o-mini', 'deepseek-chat'],
    status: 'active',
  },
  {
    id: 'plan_pro',
    name: '专业套餐',
    planType: 'token',
    price: 99,
    durationDays: 30,
    totalQuota: '5000000',
    allowedModels: ['gpt-4o', 'claude-3-5-sonnet', 'gpt-4o-mini', 'deepseek-chat', 'glm-4.5'],
    status: 'active',
  },
];

const mockUserPlans: UserPlan[] = [
  {
    id: 'up_1',
    planId: 'plan_starter',
    planType: 'token',
    totalQuota: '500000',
    remainingQuota: '371600',
    priority: 1,
    status: 'active',
    activatedAt: '2026-07-20T03:14:00Z',
    expiresAt: '2026-08-24T00:00:00Z',
  },
];

const mockSettings: UserSettings = { preferredBilling: 'token', fallbackEnabled: false };

// ---------------------------------------------------------------------------
// Real API helpers (contract-shaped responses)
// ---------------------------------------------------------------------------

async function realGetKeys(): Promise<ApiKey[]> {
  const r = await request<{ keys: ApiKey[] }>(`${BIZ_BASE}/api/v1/keys`, { baseUrl: BIZ_BASE });
  return r.keys;
}
async function realCreateKey(input: CreateKeyInput): Promise<ApiKeyCreated> {
  const r = await request<{ key: ApiKeyCreated }>(`${BIZ_BASE}/api/v1/keys`, {
    method: 'POST', body: input, baseUrl: BIZ_BASE,
  });
  return r.key;
}
async function realUpdateKey(id: string, input: UpdateKeyInput): Promise<ApiKey> {
  const r = await request<{ key: ApiKey }>(`${BIZ_BASE}/api/v1/keys/${id}`, {
    method: 'PATCH', body: input, baseUrl: BIZ_BASE,
  });
  return r.key;
}
async function realDeleteKey(id: string): Promise<void> {
  await request<void>(`${BIZ_BASE}/api/v1/keys/${id}`, { method: 'DELETE', noContent: true, baseUrl: BIZ_BASE });
}
async function realRotateKey(id: string): Promise<ApiKeyCreated> {
  const r = await request<{ key: ApiKeyCreated }>(`${BIZ_BASE}/api/v1/keys/${id}/rotate`, {
    method: 'POST', baseUrl: BIZ_BASE,
  });
  return r.key;
}
async function realListLogs(
  page: number, pageSize: number,
): Promise<{ items: RequestLog[]; total: number; page: number; pageSize: number }> {
  const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  const r = await request<{ logs: RequestLog[]; total: number; page: number; pageSize: number }>(
    `${BIZ_BASE}/api/v1/request-logs?${qs}`, { baseUrl: BIZ_BASE },
  );
  return { items: r.logs, total: r.total, page: r.page, pageSize: r.pageSize };
}
async function realBalance(): Promise<UserBalance> {
  return request<UserBalance>(`${BIZ_BASE}/api/v1/user/balance`, { baseUrl: BIZ_BASE });
}
async function realPlans(): Promise<Plan[]> {
  const r = await request<{ plans: Plan[] }>(`${BIZ_BASE}/api/v1/plans`, { baseUrl: BIZ_BASE });
  return r.plans;
}
async function realUserPlans(): Promise<UserPlan[]> {
  const r = await request<{ plans: UserPlan[] }>(`${BIZ_BASE}/api/v1/user/plans`, { baseUrl: BIZ_BASE });
  return r.plans;
}
async function realUsageStats(days = 7): Promise<UsageStats> {
  return request<UsageStats>(`${BIZ_BASE}/api/v1/request-logs/stats?days=${days}`, { baseUrl: BIZ_BASE });
}
async function realGetSettings(): Promise<UserSettings> {
  return request<UserSettings>(`${BIZ_BASE}/api/v1/user/settings`, { baseUrl: BIZ_BASE });
}
async function realUpdateSettings(input: UserSettingsUpdate): Promise<UserSettings> {
  return request<UserSettings>(`${BIZ_BASE}/api/v1/user/settings`, {
    method: 'PATCH', body: input, baseUrl: BIZ_BASE,
  });
}

// ---------------------------------------------------------------------------
// Unified surface
// ---------------------------------------------------------------------------

export const userApi = {
  // ---- API keys ----
  getKeys: async (): Promise<ApiKey[]> => {
    if (useMock) { await delay(260); return mockKeys.filter((k) => k.status === 'active'); }
    return realGetKeys();
  },
  createKey: async (input: CreateKeyInput): Promise<ApiKeyCreated> => {
    if (useMock) {
      await delay(400);
      const secret = 'tmp_' + Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
      const created: ApiKeyCreated = {
        id: `key_${Date.now()}`,
        name: input.name || '未命名密钥',
        keyPrefix: secret.slice(0, 8),
        keySuffix: secret.slice(-4),
        secret,
        status: 'active',
        lastUsedAt: null,
        expiresAt: input.expiresAt ?? null,
        createdAt: new Date().toISOString(),
      };
      mockKeys.unshift(created);
      return created;
    }
    return realCreateKey(input);
  },
  updateKey: async (id: string, input: UpdateKeyInput): Promise<ApiKey> => {
    if (useMock) {
      await delay(300);
      const k = mockKeys.find((x) => x.id === id);
      if (!k) throw new Error('not_found');
      if (input.name !== undefined) k.name = input.name;
      if (input.status !== undefined) k.status = input.status;
      return k;
    }
    return realUpdateKey(id, input);
  },
  /** Soft-delete (contract: DELETE). Mock keeps the old "revoke" semantics. */
  revokeKey: async (id: string): Promise<void> => {
    if (useMock) { await delay(350); const k = mockKeys.find((x) => x.id === id); if (k) k.status = 'disabled'; return; }
    return realDeleteKey(id);
  },
  rotateKey: async (id: string): Promise<ApiKeyCreated> => {
    if (useMock) {
      await delay(380);
      const k = mockKeys.find((x) => x.id === id);
      if (!k) throw new Error('not_found');
      const secret = 'tmp_' + Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
      const rotated: ApiKeyCreated = {
        ...k, secret, keyPrefix: secret.slice(0, 8), keySuffix: secret.slice(-4),
      };
      return rotated;
    }
    return realRotateKey(id);
  },

  // ---- Request logs ----
  getRecentRequests: async (limit = 5): Promise<RequestLog[]> => {
    if (useMock) { await delay(220); return allMockLogs.slice(0, limit); }
    const r = await realListLogs(1, limit);
    return r.items;
  },
  getRequests: async (
    page = 1, pageSize = 10,
  ): Promise<{ items: RequestLog[]; total: number }> => {
    if (useMock) {
      await delay(300);
      const start = (page - 1) * pageSize;
      return { items: allMockLogs.slice(start, start + pageSize), total: allMockLogs.length };
    }
    const r = await realListLogs(page, pageSize);
    return { items: r.items, total: r.total };
  },

  // ---- Balance / plans / usage ----
  getBalance: async (): Promise<UserBalance> => {
    if (useMock) { await delay(280); return mockBalance; }
    return realBalance();
  },
  getPlans: async (): Promise<Plan[]> => {
    if (useMock) { await delay(260); return mockPlans; }
    return realPlans();
  },
  getUserPlans: async (): Promise<UserPlan[]> => {
    if (useMock) { await delay(260); return mockUserPlans; }
    return realUserPlans();
  },
  getUsageStats: async (days = 7): Promise<UsageStats> => {
    if (useMock) {
      await delay(300);
      return {
        days,
        totalRequests: 47,
        totalInputTokens: '12400',
        totalOutputTokens: '18900',
        totalCost: '1.2340',
        byModel: mockModels.slice(0, 3).map((m, i) => ({
          model: m!, requests: 10 + i * 3,
          inputTokens: String(3000 + i * 1000),
          outputTokens: String(5000 + i * 1500),
          cost: (0.3 + i * 0.2).toFixed(4),
        })),
      };
    }
    return realUsageStats(days);
  },

  // ---- Settings ----
  getSettings: async (): Promise<UserSettings> => {
    if (useMock) { await delay(180); return { ...mockSettings }; }
    return realGetSettings();
  },
  updateSettings: async (input: UserSettingsUpdate): Promise<UserSettings> => {
    if (useMock) { await delay(220); Object.assign(mockSettings, input); return { ...mockSettings }; }
    return realUpdateSettings(input);
  },
};

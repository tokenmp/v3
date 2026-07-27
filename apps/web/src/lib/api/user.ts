/** User business API: API keys, request logs, plans, balance, usage, settings.
 *  All methods call the real BFF (services/api Edge) via BIZ_BASE. */

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

const BIZ_BASE = process.env.NEXT_PUBLIC_BIZ_API_BASE ?? API_BASE;

export const userApi = {
  // ---- API keys ----
  getKeys: async (): Promise<ApiKey[]> => {
    const r = await request<{ keys: ApiKey[] }>(`/api/v1/keys`, { baseUrl: BIZ_BASE });
    return r.keys;
  },
  createKey: async (input: CreateKeyInput): Promise<ApiKeyCreated> => {
    const r = await request<{ key: ApiKeyCreated }>(`/api/v1/keys`, {
      method: 'POST', body: input, baseUrl: BIZ_BASE,
    });
    return r.key;
  },
  updateKey: async (id: string, input: UpdateKeyInput): Promise<ApiKey> => {
    const r = await request<{ key: ApiKey }>(`/api/v1/keys/${id}`, {
      method: 'PATCH', body: input, baseUrl: BIZ_BASE,
    });
    return r.key;
  },
  revokeKey: async (id: string): Promise<void> => {
    await request<void>(`/api/v1/keys/${id}`, { method: 'DELETE', noContent: true, baseUrl: BIZ_BASE });
  },
  rotateKey: async (id: string): Promise<ApiKeyCreated> => {
    const r = await request<{ key: ApiKeyCreated }>(`/api/v1/keys/${id}/rotate`, {
      method: 'POST', baseUrl: BIZ_BASE,
    });
    return r.key;
  },

  // ---- Request logs ----
  getRecentRequests: async (limit = 5): Promise<RequestLog[]> => {
    const r = await request<{ logs: RequestLog[]; total: number }>(
      `/api/v1/request-logs?page=1&pageSize=${limit}`, { baseUrl: BIZ_BASE },
    );
    return r.logs ?? [];
  },
  getRequests: async (
    page = 1, pageSize = 10,
  ): Promise<{ items: RequestLog[]; total: number }> => {
    const qs = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
    const r = await request<{ logs: RequestLog[]; total: number }>(
      `/api/v1/request-logs?${qs}`, { baseUrl: BIZ_BASE },
    );
    return { items: r.logs ?? [], total: r.total ?? 0 };
  },

  // ---- Balance / plans / usage ----
  getBalance: async (): Promise<UserBalance> => {
    return request<UserBalance>(`/api/v1/user/balance`, { baseUrl: BIZ_BASE });
  },
  getPlans: async (): Promise<Plan[]> => {
    const r = await request<{ plans: Plan[] }>(`/api/v1/plans`, { baseUrl: BIZ_BASE });
    return r.plans ?? [];
  },
  getUserPlans: async (): Promise<UserPlan[]> => {
    const r = await request<{ plans: UserPlan[] }>(`/api/v1/user/plans`, { baseUrl: BIZ_BASE });
    return r.plans ?? [];
  },
  getUsageStats: async (days = 7): Promise<UsageStats> => {
    return request<UsageStats>(`/api/v1/request-logs/stats?days=${days}`, { baseUrl: BIZ_BASE });
  },

  // ---- Settings ----
  getSettings: async (): Promise<UserSettings> => {
    return request<UserSettings>(`/api/v1/user/settings`, { baseUrl: BIZ_BASE });
  },
  updateSettings: async (input: UserSettingsUpdate): Promise<UserSettings> => {
    return request<UserSettings>(`/api/v1/user/settings`, {
      method: 'PATCH', body: input, baseUrl: BIZ_BASE,
    });
  },
  // Per-user auto model pool override. model_ids is the ordered list of model
  // IDs to use when calling model=auto; an empty array resets to platform
  // default. Returns the stored list.
  getAutoModels: async (): Promise<string[]> => {
    const res = await request<{ model_ids: string[] }>(`/api/v1/user/auto-models`, { baseUrl: BIZ_BASE });
    return res.model_ids ?? [];
  },
  updateAutoModels: async (modelIds: string[]): Promise<string[]> => {
    const res = await request<{ model_ids: string[] }>(`/api/v1/user/auto-models`, {
      method: 'PATCH', body: { model_ids: modelIds }, baseUrl: BIZ_BASE,
    });
    return res.model_ids ?? [];
  },
};

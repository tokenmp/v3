/** Admin API: dashboard stats, users, api-keys, request-logs.
 *  All methods call the real Edge admin endpoints via BIZ_BASE.
 *  The Edge requires role=admin JWT claims. Components are agnostic
 *  to the source. */

import type {
  AdminApiKey,
  AdminDashboardStats,
  AdminRequestLog,
  AdminRequestLogListResponse,
  AdminUser,
  AdminUserDetail,
  AdminUserListResponse,
  AdminAnnouncement,
  AdminAnnouncementInput,
  AdminChangelog,
  AdminChangelogInput,
  AdminNotification,
  AdminNotificationInput,
  AdminPlan,
  AdminPlanInput,
  AdminUserPlan,
  AdminUserPlanInput,
  AdminProvider,
  AdminModelConfig,
  AdminRouteConfig,
  AdminUpstreamCredential,
} from '@/types/admin';
import { request, API_BASE, NOTICE_BASE } from './core';

const ADMIN_BASE = process.env.NEXT_PUBLIC_BIZ_API_BASE ?? API_BASE;

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

async function realDashboard(days = 15): Promise<AdminDashboardStats> {
  return request<AdminDashboardStats>(`/api/v1/admin/stats?days=${days}`, { baseUrl: ADMIN_BASE });
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

async function realListUsers(
  page = 1,
  pageSize = 20,
  search = '',
): Promise<AdminUserListResponse> {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  if (search) params.set('search', search);
  const res = await request<{ users: AdminUser[]; total: number }>(
    `/api/v1/admin/users?${params}`,
    { baseUrl: ADMIN_BASE },
  );
  return { users: res.users ?? [], total: res.total ?? 0, page, pageSize };
}

async function realGetUser(id: string): Promise<AdminUserDetail> {
  return request<AdminUserDetail>(`/api/v1/admin/users/${id}`, { baseUrl: ADMIN_BASE });
}

async function realUpdateUser(id: string, input: { status?: string; role?: string }): Promise<AdminUser> {
  return request<AdminUser>(`/api/v1/admin/users/${id}`, {
    method: 'PATCH',
    body: input,
    baseUrl: ADMIN_BASE,
  });
}

async function realListKeys(page = 1, pageSize = 20): Promise<{ keys: AdminApiKey[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  const res = await request<{ keys: AdminApiKey[]; total: number }>(
    `/api/v1/admin/keys?${params}`,
    { baseUrl: ADMIN_BASE },
  );
  return { keys: res.keys ?? [], total: res.total ?? 0 };
}

async function realListLogs(
  page = 1,
  pageSize = 20,
): Promise<AdminRequestLogListResponse> {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  const res = await request<{ logs: AdminRequestLog[]; total: number }>(
    `/api/v1/admin/request-logs?${params}`,
    { baseUrl: ADMIN_BASE },
  );
  return { logs: res.logs ?? [], total: res.total ?? 0, page, pageSize };
}

async function realGetLog(id: string): Promise<AdminRequestLog> {
  return request<AdminRequestLog>(`/api/v1/admin/request-logs/${id}`, { baseUrl: ADMIN_BASE });
}

// ---------------------------------------------------------------------------
// Snake_case → camelCase mapper for admin API responses
// ---------------------------------------------------------------------------

function mapUser(u: Record<string, unknown>): AdminUser {
  return {
    id: String(u.id ?? u.user_id ?? ''),
    email: String(u.email ?? ''),
    role: (u.role as AdminUser['role']) ?? 'user',
    status: (u.status as AdminUser['status']) ?? 'active',
    createdAt: String(u.created_at ?? u.createdAt ?? ''),
  };
}

function mapKey(k: Record<string, unknown>): AdminApiKey {
  return {
    id: String(k.id ?? k.key_id ?? ''),
    name: String(k.name ?? ''),
    keyPrefix: String(k.key_prefix ?? k.keyPrefix ?? ''),
    keySuffix: String(k.key_suffix ?? k.keySuffix ?? ''),
    status: (k.status as AdminApiKey['status']) ?? 'active',
    createdAt: String(k.created_at ?? k.createdAt ?? ''),
    lastUsedAt: k.last_used_at ? String(k.last_used_at) : (k.lastUsedAt ? String(k.lastUsedAt) : null),
    expiresAt: k.expires_at ? String(k.expires_at) : (k.expiresAt ? String(k.expiresAt) : null),
    userEmail: String(k.user_email ?? k.userEmail ?? ''),
  } as AdminApiKey;
}

// ---------------------------------------------------------------------------
// Unified admin API surface (no mock — real backend only)
// ---------------------------------------------------------------------------

export const adminApi = {
  // ---- Dashboard ----
  getDashboardStats: async (days = 15): Promise<AdminDashboardStats> => realDashboard(days),

  // ---- Users ----
  listUsers: async (page = 1, pageSize = 20, search = ''): Promise<AdminUserListResponse> => {
    const res = await realListUsers(page, pageSize, search);
    // Map snake_case → camelCase if the backend returns snake_case.
    const raw = res.users as unknown as Record<string, unknown>[];
    const isSnake = raw.length > 0 && raw[0] && 'created_at' in (raw[0] as object) && !('createdAt' in (raw[0] as object));
    return { ...res, users: isSnake ? raw.map((u) => mapUser(u)) : res.users };
  },

  getUser: realGetUser,

  updateUser: realUpdateUser,

  // ---- API Keys ----
  listKeys: async (page = 1, pageSize = 20): Promise<{ keys: AdminApiKey[]; total: number }> => {
    const res = await realListKeys(page, pageSize);
    const raw = res.keys as unknown as Record<string, unknown>[];
    const isSnake = raw.length > 0 && raw[0] && 'key_prefix' in (raw[0] as object) && !('keyPrefix' in (raw[0] as object));
    return { ...res, keys: isSnake ? raw.map((k) => mapKey(k)) : res.keys };
  },

  // ---- Request logs ----
  listRequestLogs: realListLogs,
  getRequestLog: realGetLog,

  // ---- Announcements (Notice service via Edge or direct) ----
  announcements: {
    list: async (): Promise<AdminAnnouncement[]> => {
      const res = await request<{ items: AdminAnnouncement[] } | AdminAnnouncement[]>(
        '/api/v1/admin/announcements',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items.map((a) => mapAnnouncement(a as unknown as Record<string, unknown>));
    },
    create: async (input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
      const res = await request<AdminAnnouncement>('/api/v1/admin/announcements', {
        method: 'POST',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return res;
    },
    update: async (id: string, input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
      await request<{ id: string }>('/api/v1/admin/announcements/' + id, {
        method: 'PATCH',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      // Backend returns {id}; synthesize the full object.
      return { ...input, id, publishedAt: input.publishedAt, createdAt: '', updatedAt: '' } as AdminAnnouncement;
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/admin/announcements/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
    publish: async (id: string): Promise<AdminAnnouncement> => {
      await request<void>('/api/v1/admin/announcements/' + id + '/publish', {
        method: 'POST',
        baseUrl: NOTICE_BASE,
      });
      return { id, publishedAt: new Date().toISOString() } as AdminAnnouncement;
    },
  },

  // ---- Changelogs ----
  changelogs: {
    list: async (): Promise<AdminChangelog[]> => {
      const res = await request<{ items: AdminChangelog[] } | AdminChangelog[]>(
        '/api/v1/admin/changelogs',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items.map((c) => mapChangelog(c as unknown as Record<string, unknown>));
    },
    create: async (input: AdminChangelogInput): Promise<AdminChangelog> => {
      const res = await request<AdminChangelog>('/api/v1/admin/changelogs', {
        method: 'POST',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return res;
    },
    update: async (id: string, input: AdminChangelogInput): Promise<AdminChangelog> => {
      await request<{ id: string }>('/api/v1/admin/changelogs/' + id, {
        method: 'PATCH',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return { ...input, id, publishedAt: input.publishedAt, createdAt: '', updatedAt: '' } as AdminChangelog;
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/admin/changelogs/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
    publish: async (id: string): Promise<AdminChangelog> => {
      await request<void>('/api/v1/admin/changelogs/' + id + '/publish', {
        method: 'POST',
        baseUrl: NOTICE_BASE,
      });
      return { id, publishedAt: new Date().toISOString() } as AdminChangelog;
    },
  },

  // ---- Notifications ----
  notifications: {
    list: async (): Promise<AdminNotification[]> => {
      const res = await request<{ items: AdminNotification[] } | AdminNotification[]>(
        '/api/v1/admin/notifications',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items.map((n) => mapNotification(n as unknown as Record<string, unknown>));
    },
    send: async (input: AdminNotificationInput): Promise<AdminNotification> => {
      const res = await request<{ id: string; accepted: boolean; queuedAt: string }>(
        '/api/v1/admin/notifications/send',
        { method: 'POST', body: input, baseUrl: NOTICE_BASE },
      );
      return {
        id: res.id,
        userId: input.userId ?? '',
        type: input.type,
        title: input.title,
        body: input.body,
        action: input.action,
        readAt: null,
        createdAt: res.queuedAt ?? new Date().toISOString(),
      };
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/admin/notifications/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
  },

  // ---- Plans (Billing) ----
  plans: {
    list: async (): Promise<AdminPlan[]> => {
      const res = await request<{ items: AdminPlan[] } | AdminPlan[]>(
        '/api/v1/admin/plans',
        { baseUrl: ADMIN_BASE },
      );
      return Array.isArray(res) ? res : (res.items ?? []);
    },
    create: async (input: AdminPlanInput): Promise<AdminPlan> => {
      return request<AdminPlan>('/api/v1/admin/plans', {
        method: 'POST',
        body: input,
        baseUrl: ADMIN_BASE,
      });
    },
    update: async (id: string, input: AdminPlanInput): Promise<AdminPlan> => {
      return request<AdminPlan>('/api/v1/admin/plans/' + id, {
        method: 'PATCH',
        body: input,
        baseUrl: ADMIN_BASE,
      });
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/admin/plans/' + id, {
        method: 'DELETE',
        baseUrl: ADMIN_BASE,
      });
    },
  },

  // ---- User plans ----
  userPlans: {
    list: async (): Promise<AdminUserPlan[]> => {
      const res = await request<{ items: AdminUserPlan[] } | AdminUserPlan[]>(
        '/api/v1/admin/user-plans',
        { baseUrl: ADMIN_BASE },
      );
      return Array.isArray(res) ? res : (res.items ?? []);
    },
    assign: async (input: AdminUserPlanInput): Promise<AdminUserPlan> => {
      return request<AdminUserPlan>('/api/v1/admin/user-plans', {
        method: 'POST',
        body: input,
        baseUrl: ADMIN_BASE,
      });
    },
    cancel: async (id: string): Promise<void> => {
      await request<void>(`/api/v1/admin/user-plans/${id}/cancel`, {
        method: 'POST',
        baseUrl: ADMIN_BASE,
      });
    },
  },

  // ---- Usage stats ----
  usageStats: async (): Promise<Record<string, unknown>> => {
    return request<Record<string, unknown>>('/api/v1/admin/usage/stats', { baseUrl: ADMIN_BASE });
  },

  // ---- Model catalog (for plan allowedModels selector) — issue #89 ----
  listModels: async (): Promise<string[]> => {
    try {
      return await request<string[]>('/api/v1/admin/models/catalog', { baseUrl: ADMIN_BASE });
    } catch {
      // Fallback: return empty list if Config Service is not wired.
      return [];
    }
  },
};

// Backward-compatible aliases for pages that import adminPlanApi / adminUserPlanApi.
export const adminPlanApi = {
  list: adminApi.plans.list,
  create: adminApi.plans.create,
  update: adminApi.plans.update,
  delete: adminApi.plans.delete,
  listModels: adminApi.listModels,
};

export const adminUserPlanApi = {
  list: adminApi.userPlans.list,
  assign: adminApi.userPlans.assign,
  cancel: adminApi.userPlans.cancel,
};

export const adminAnnouncementApi = {
  list: adminApi.announcements.list,
  create: adminApi.announcements.create,
  update: adminApi.announcements.update,
  delete: adminApi.announcements.delete,
  publish: adminApi.announcements.publish,
};

export const adminChangelogApi = {
  list: adminApi.changelogs.list,
  create: adminApi.changelogs.create,
  update: adminApi.changelogs.update,
  delete: adminApi.changelogs.delete,
  publish: adminApi.changelogs.publish,
};

export const adminNotificationApi = {
  list: adminApi.notifications.list,
  send: adminApi.notifications.send,
  delete: adminApi.notifications.delete,
};

// ---------------------------------------------------------------------------
// Config admin API: providers, models, routes (full CRUD via Config Service
// proxied through Edge). No mock — real backend only.
// ---------------------------------------------------------------------------

function mapProvider(p: Record<string, unknown>): AdminProvider {
  return {
    id: String(p.id ?? ''),
    name: String(p.name ?? ''),
    displayLabel: String(p.display_label ?? p.displayLabel ?? ''),
    selector: String(p.selector ?? ''),
    baseURL: String(p.base_url ?? p.baseURL ?? ''),
    sdkKind: (p.sdk_kind ?? p.sdkKind ?? 'openai') as AdminProvider['sdkKind'],
    protocol: String(p.protocol ?? ''),
    status: (p.status ?? 'active') as AdminProvider['status'],
    credentialCount: Number(p.credential_count ?? p.credentialCount ?? 0),
    routeCount: Number(p.route_count ?? p.routeCount ?? 0),
  };
}

function mapModelConfig(m: Record<string, unknown>): AdminModelConfig {
  const caps = m.capabilities;
  let capabilities: string[] = [];
  if (Array.isArray(caps)) capabilities = caps.map(String);
  else if (typeof caps === 'string') {
    try { capabilities = JSON.parse(caps) as string[]; } catch { capabilities = []; }
  }
  return {
    id: String(m.id ?? ''),
    displayName: String(m.display_name ?? m.displayName ?? ''),
    capabilities,
    thinkingSupported: Boolean(m.thinking_supported ?? m.thinkingSupported ?? false),
    routeCount: Number(m.route_count ?? m.routeCount ?? 0),
  };
}

function mapRouteConfig(r: Record<string, unknown>): AdminRouteConfig {
  return {
    id: String(r.id ?? ''),
    modelId: String(r.model_id ?? r.modelId ?? ''),
    providerId: String(r.provider_id ?? r.providerId ?? ''),
    upstreamModel: String(r.upstream_model ?? r.upstreamModel ?? ''),
    protocol: String(r.protocol ?? ''),
    priority: Number(r.priority ?? 0),
    enabled: Boolean(r.enabled ?? false),
    quarantined: Boolean(r.quarantined ?? false),
  };
}

function mapCredential(c: Record<string, unknown>): AdminUpstreamCredential {
  return {
    id: String(c.id ?? ''),
    providerId: String(c.provider_id ?? c.providerId ?? ''),
    credentialRef: String(c.credential_ref ?? c.credentialRef ?? ''),
    keyPrefix: c.key_prefix != null ? String(c.key_prefix) : (c.keyPrefix != null ? String(c.keyPrefix) : null),
    keySuffix: c.key_suffix != null ? String(c.key_suffix) : (c.keySuffix != null ? String(c.keySuffix) : null),
    priority: Number(c.priority ?? 0),
    maxConcurrency: c.max_concurrency != null ? Number(c.max_concurrency) : (c.maxConcurrency != null ? Number(c.maxConcurrency) : null),
    dailyQuota: c.daily_quota != null ? Number(c.daily_quota) : (c.dailyQuota != null ? Number(c.dailyQuota) : null),
    status: (c.status ?? 'active') as AdminUpstreamCredential['status'],
    createdAt: String(c.created_at ?? c.createdAt ?? ''),
    updatedAt: String(c.updated_at ?? c.updatedAt ?? ''),
  };
}

export const adminConfigApi = {
  // ---- Providers ----
  listProviders: async (): Promise<AdminProvider[]> => {
    const res = await request<{ items: AdminProvider[] } | AdminProvider[]>(
      '/api/v1/admin/providers',
      { baseUrl: ADMIN_BASE },
    );
    const items = Array.isArray(res) ? res : (res.items ?? []);
    return items.map((p) => mapProvider(p as unknown as Record<string, unknown>));
  },
  createProvider: async (input: Partial<AdminProvider> & { id: string; name: string; baseURL: string; sdkKind: string; protocol: string }): Promise<AdminProvider> => {
    return request<AdminProvider>('/api/v1/admin/providers', {
      method: 'POST',
      body: {
        id: input.id,
        name: input.name,
        display_label: input.displayLabel ?? input.name,
        selector: input.selector ?? input.id,
        base_url: input.baseURL,
        sdk_kind: input.sdkKind,
        protocol: input.protocol,
        status: input.status ?? 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateProvider: async (id: string, input: Partial<AdminProvider>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.name !== undefined) fields.name = input.name;
    if (input.displayLabel !== undefined) fields.display_label = input.displayLabel;
    if (input.baseURL !== undefined) fields.base_url = input.baseURL;
    if (input.sdkKind !== undefined) fields.sdk_kind = input.sdkKind;
    if (input.protocol !== undefined) fields.protocol = input.protocol;
    if (input.status !== undefined) fields.status = input.status;
    await request<{ id: string }>(`/api/v1/admin/providers/${id}`, {
      method: 'PATCH',
      body: fields,
      baseUrl: ADMIN_BASE,
    });
  },
  deleteProvider: async (id: string): Promise<void> => {
    await request<void>(`/api/v1/admin/providers/${id}`, {
      method: 'DELETE',
      baseUrl: ADMIN_BASE,
    });
  },

  // ---- Models ----
  listModels: async (): Promise<AdminModelConfig[]> => {
    const res = await request<{ items: AdminModelConfig[] } | AdminModelConfig[]>(
      '/api/v1/admin/models',
      { baseUrl: ADMIN_BASE },
    );
    const items = Array.isArray(res) ? res : (res.items ?? []);
    return items.map((m) => mapModelConfig(m as unknown as Record<string, unknown>));
  },
  createModel: async (input: { id: string; displayName: string; capabilities?: string[]; thinkingSupported?: boolean }): Promise<AdminModelConfig> => {
    return request<AdminModelConfig>('/api/v1/admin/models', {
      method: 'POST',
      body: {
        id: input.id,
        display_name: input.displayName,
        capabilities: input.capabilities ?? ['text'],
        thinking_supported: input.thinkingSupported ?? false,
        status: 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateModel: async (id: string, input: Partial<AdminModelConfig>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.displayName !== undefined) fields.display_name = input.displayName;
    if (input.thinkingSupported !== undefined) fields.thinking_supported = input.thinkingSupported;
    await request<{ id: string }>(`/api/v1/admin/models/${id}`, {
      method: 'PATCH',
      body: fields,
      baseUrl: ADMIN_BASE,
    });
  },
  deleteModel: async (id: string): Promise<void> => {
    await request<void>(`/api/v1/admin/models/${id}`, {
      method: 'DELETE',
      baseUrl: ADMIN_BASE,
    });
  },

  // ---- Routes ----
  listRoutes: async (): Promise<AdminRouteConfig[]> => {
    const res = await request<{ items: AdminRouteConfig[] } | AdminRouteConfig[]>(
      '/api/v1/admin/routes',
      { baseUrl: ADMIN_BASE },
    );
    const items = Array.isArray(res) ? res : (res.items ?? []);
    return items.map((r) => mapRouteConfig(r as unknown as Record<string, unknown>));
  },
  createRoute: async (input: { id: string; modelId: string; providerId: string; upstreamModel: string; protocol: string; priority?: number }): Promise<AdminRouteConfig> => {
    return request<AdminRouteConfig>('/api/v1/admin/routes', {
      method: 'POST',
      body: {
        id: input.id,
        model_id: input.modelId,
        provider_id: input.providerId,
        upstream_model: input.upstreamModel,
        protocol: input.protocol,
        priority: input.priority ?? 0,
        enabled: true,
        is_default: false,
        status: 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateRoute: async (id: string, input: Partial<AdminRouteConfig>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.upstreamModel !== undefined) fields.upstream_model = input.upstreamModel;
    if (input.priority !== undefined) fields.priority = input.priority;
    if (input.enabled !== undefined) fields.enabled = input.enabled;
    await request<{ id: string }>(`/api/v1/admin/routes/${id}`, {
      method: 'PATCH',
      body: fields,
      baseUrl: ADMIN_BASE,
    });
  },
  deleteRoute: async (id: string): Promise<void> => {
    await request<void>(`/api/v1/admin/routes/${id}`, {
      method: 'DELETE',
      baseUrl: ADMIN_BASE,
    });
  },

  // ---- Upstream Credentials (上游账号) ----
  listCredentials: async (providerId: string): Promise<AdminUpstreamCredential[]> => {
    const res = await request<{ items: AdminUpstreamCredential[] } | AdminUpstreamCredential[]>(
      `/api/v1/admin/providers/${providerId}/credentials`,
      { baseUrl: ADMIN_BASE },
    );
    const items = Array.isArray(res) ? res : (res.items ?? []);
    return items.map((c) => mapCredential(c as unknown as Record<string, unknown>));
  },
  listAllCredentials: async (): Promise<AdminUpstreamCredential[]> => {
    // Fetch credentials across all providers by first listing providers
    const providers = await adminConfigApi.listProviders();
    const results = await Promise.all(
      providers.map((p) => adminConfigApi.listCredentials(p.id)),
    );
    return results.flat();
  },
  createCredential: async (providerId: string, input: {
    id: string;
    apiKey: string;
    priority?: number;
    maxConcurrency?: number | null;
    dailyQuota?: number | null;
    status?: string;
  }): Promise<AdminUpstreamCredential> => {
    return request<AdminUpstreamCredential>(`/api/v1/admin/providers/${providerId}/credentials`, {
      method: 'POST',
      body: {
        id: input.id,
        api_key: input.apiKey,
        priority: input.priority ?? 0,
        max_concurrency: input.maxConcurrency ?? null,
        daily_quota: input.dailyQuota ?? null,
        status: input.status ?? 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateCredential: async (id: string, input: Partial<AdminUpstreamCredential>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.apiKey !== undefined && input.apiKey !== '') fields.api_key = input.apiKey;
    if (input.priority !== undefined) fields.priority = input.priority;
    if (input.maxConcurrency !== undefined) fields.max_concurrency = input.maxConcurrency;
    if (input.dailyQuota !== undefined) fields.daily_quota = input.dailyQuota;
    if (input.status !== undefined) fields.status = input.status;
    await request<{ id: string }>(`/api/v1/admin/credentials/${id}`, {
      method: 'PATCH',
      body: fields,
      baseUrl: ADMIN_BASE,
    });
  },
  deleteCredential: async (id: string): Promise<void> => {
    await request<void>(`/api/v1/admin/credentials/${id}`, {
      method: 'DELETE',
      baseUrl: ADMIN_BASE,
    });
  },
};

// ---------------------------------------------------------------------------
// Mappers (snake_case → camelCase)
// ---------------------------------------------------------------------------

function mapAnnouncement(a: Record<string, unknown>): AdminAnnouncement {
  return {
    id: String(a.id ?? ''),
    title: String(a.title ?? ''),
    summary: String(a.summary ?? ''),
    body: String(a.body ?? ''),
    severity: (a.severity ?? 'info') as AdminAnnouncement['severity'],
    publishedAt: a.published_at ? String(a.published_at) : (a.publishedAt ? String(a.publishedAt) : null),
    createdAt: String(a.created_at ?? a.createdAt ?? ''),
    updatedAt: String(a.updated_at ?? a.updatedAt ?? ''),
  };
}

function mapChangelog(c: Record<string, unknown>): AdminChangelog {
  return {
    id: String(c.id ?? ''),
    version: String(c.version ?? ''),
    title: String(c.title ?? ''),
    body: String(c.body ?? ''),
    publishedAt: c.published_at ? String(c.published_at) : (c.publishedAt ? String(c.publishedAt) : null),
    createdAt: String(c.created_at ?? c.createdAt ?? ''),
    updatedAt: String(c.updated_at ?? c.updatedAt ?? ''),
  };
}

function mapNotification(n: Record<string, unknown>): AdminNotification {
  return {
    id: String(n.id ?? ''),
    userId: String(n.user_id ?? n.userId ?? n.UserID ?? ''),
    type: String(n.type ?? ''),
    title: String(n.title ?? ''),
    body: String(n.body ?? ''),
    action: (n.action ?? null) as AdminNotification['action'],
    readAt: n.read_at ? String(n.read_at) : (n.readAt ? String(n.readAt) : null),
    createdAt: String(n.created_at ?? n.createdAt ?? ''),
  };
}

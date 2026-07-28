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
  AdminUpstreamEndpoint,
  AdminGlobalPolicy,
  RetryPolicy,
  RetryAction,
  RetryRule,
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
  status = '',
  role = '',
): Promise<AdminUserListResponse> {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  if (search) params.set('search', search);
  if (status) params.set('status', status);
  if (role) params.set('role', role);
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
  const raw = (res.logs ?? []) as unknown as Record<string, unknown>[];
  return { logs: raw.map(mapRequestLog), total: res.total ?? 0, page, pageSize };
}

async function realGetLog(id: string): Promise<AdminRequestLog> {
  // The detail endpoint returns { log: {...}, attempts: [...] }; unwrap the
  // log object and attach attempts so mapRequestLog maps snake_case fields.
  const res = await request<{ log?: Record<string, unknown>; attempts?: unknown[] } & Record<string, unknown>>(
    `/api/v1/admin/request-logs/${id}`,
    { baseUrl: ADMIN_BASE },
  );
  const log = res.log ?? res;
  const merged: Record<string, unknown> = { ...log };
  if (Array.isArray(res.attempts)) merged.attempts = res.attempts;
  return mapRequestLog(merged);
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

function mapRequestLogAttempt(a: Record<string, unknown>): Record<string, unknown> {
  return {
    ...a,
    attemptIndex: a.attempt_index ?? a.attemptIndex,
    upstreamModel: a.upstream_model ?? a.upstreamModel,
    httpStatus: a.http_status ?? a.httpStatus,
    upstreamHttpStatus: a.upstream_http_status ?? a.upstreamHttpStatus ?? a.upstreamHttpStatus,
    latencyMs: a.latency_ms ?? a.latencyMs,
    errorCode: a.error_code ?? a.errorCode,
    errorType: a.error_type ?? a.errorType,
    retryClassified: a.retry_classified ?? a.retryClassified,
    metadata: a.metadata,
  };
}

function mapRequestLog(r: Record<string, unknown>): AdminRequestLog {
  const rawStatus = String(r.final_status ?? r.status ?? '');
  let status: 'success' | 'error' = 'error';
  if (rawStatus === 'success') status = 'success';

  const rawAttempts = r.attempts ?? r.events;
  const attempts = Array.isArray(rawAttempts)
    ? rawAttempts.map((a: Record<string, unknown>) => mapRequestLogAttempt(a))
    : undefined;

  const base: AdminRequestLog = {
    requestId: String(r.request_id ?? r.requestId ?? ''),
    userId: r.user_id != null && String(r.user_id) !== '' ? String(r.user_id) : null,
    userEmail: r.user_email != null && String(r.user_email) !== '' ? String(r.user_email) : null,
    model: String(r.resolved_model ?? r.model ?? ''),
    status,
    inputTokens: r.input_tokens != null ? Number(r.input_tokens) : null,
    outputTokens: r.output_tokens != null ? Number(r.output_tokens) : null,
    totalTokens: r.total_tokens != null ? Number(r.total_tokens) : null,
    cost: null,
    durationMs: r.latency_ms != null ? Number(r.latency_ms) : null,
    createdAt: String(r.created_at ?? r.createdAt ?? ''),
    provider: r.provider_id != null ? String(r.provider_id) : null,
    protocol: r.protocol != null ? String(r.protocol) : null,
    stream: r.stream != null ? Boolean(r.stream) : null,
    httpStatus: r.http_status != null ? Number(r.http_status) : null,
    upstreamHttpStatus: r.upstream_http_status != null ? Number(r.upstream_http_status) : null,
    errorCode: r.error_code != null ? String(r.error_code) : null,
    errorType: r.error_type != null ? String(r.error_type) : null,
    errorMessage: r.error_message != null ? String(r.error_message) : null,
    billingPlan: r.billing_plan != null ? String(r.billing_plan) : null,
  };

  // For detail responses (realGetLog), attach the mapped attempts array.
  if (attempts) {
    return { ...base, attempts } as AdminRequestLog;
  }

  return base;
}

function mapPlan(p: Record<string, unknown>): AdminPlan {
  const allowedModels = p.allowed_models;
  let parsedModels: string[] = [];
  if (Array.isArray(allowedModels)) parsedModels = allowedModels.map(String);
  else if (typeof allowedModels === 'string') {
    try { parsedModels = JSON.parse(allowedModels) as string[]; } catch { parsedModels = []; }
  }
  return {
    id: String(p.id ?? ''),
    name: String(p.name ?? ''),
    planType: (p.plan_type ?? p.planType ?? 'free') as AdminPlan['planType'],
    price: Number(p.price ?? 0),
    category: (p.category ?? 'monthly') as AdminPlan['category'],
    monthlyLimit: p.monthly_limit != null ? Number(p.monthly_limit) : (p.monthlyLimit != null ? Number(p.monthlyLimit) : null),
    tokenLimit: p.token_limit != null ? Number(p.token_limit) : (p.tokenLimit != null ? Number(p.tokenLimit) : null),
    allowedModels: parsedModels,
    status: (p.status ?? 'active') as AdminPlan['status'],
    createdAt: String(p.created_at ?? p.createdAt ?? ''),
    updatedAt: String(p.updated_at ?? p.updatedAt ?? ''),
  };
}

function mapUserPlan(u: Record<string, unknown>): AdminUserPlan {
  return {
    id: String(u.id ?? ''),
    userId: String(u.user_id ?? u.userId ?? ''),
    userEmail: String(u.user_email ?? u.userEmail ?? ''),
    planId: String(u.plan_id ?? u.planId ?? ''),
    planName: String(u.plan_name ?? u.planName ?? ''),
    planType: (u.plan_type ?? u.planType ?? 'free') as AdminUserPlan['planType'],
    status: (u.status ?? 'active') as AdminUserPlan['status'],
    activatedAt: String(u.activated_at ?? u.activatedAt ?? ''),
    expiresAt: u.expires_at != null ? String(u.expires_at) : (u.expiresAt != null ? String(u.expiresAt) : null),
    remainingQuota: String(u.remaining_quota ?? u.remainingQuota ?? ''),
  };
}

// ---------------------------------------------------------------------------
// Unified admin API surface (no mock — real backend only)
// ---------------------------------------------------------------------------

export const adminApi = {
  // ---- Dashboard ----
  getDashboardStats: async (days = 15): Promise<AdminDashboardStats> => realDashboard(days),

  // ---- Users ----
  listUsers: async (page = 1, pageSize = 20, search = '', status = '', role = ''): Promise<AdminUserListResponse> => {
    const res = await realListUsers(page, pageSize, search, status, role);
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
        '/api/v1/notice/admin/announcements',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      const mapped = items.map((a) => mapAnnouncement(a as unknown as Record<string, unknown>));
      // Deterministic newest-first: backend orders by created_at DESC, id DESC,
      // but sort client-side too so a stale cache never shows a confusing order.
      return mapped.sort((a, b) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0));
    },
    create: async (input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
      const res = await request<AdminAnnouncement>('/api/v1/notice/admin/announcements', {
        method: 'POST',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return res;
    },
    update: async (id: string, input: AdminAnnouncementInput): Promise<AdminAnnouncement> => {
      await request<{ id: string }>('/api/v1/notice/admin/announcements/' + id, {
        method: 'PATCH',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      // Backend returns {id}; synthesize the full object.
      return { ...input, id, publishedAt: input.publishedAt, createdAt: '', updatedAt: '' } as AdminAnnouncement;
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/notice/admin/announcements/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
    publish: async (id: string): Promise<AdminAnnouncement> => {
      await request<void>('/api/v1/notice/admin/announcements/' + id + '/publish', {
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
        '/api/v1/notice/admin/changelogs',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      const mapped = items.map((c) => mapChangelog(c as unknown as Record<string, unknown>));
      return mapped.sort((a, b) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0));
    },
    create: async (input: AdminChangelogInput): Promise<AdminChangelog> => {
      const res = await request<AdminChangelog>('/api/v1/notice/admin/changelogs', {
        method: 'POST',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return res;
    },
    update: async (id: string, input: AdminChangelogInput): Promise<AdminChangelog> => {
      await request<{ id: string }>('/api/v1/notice/admin/changelogs/' + id, {
        method: 'PATCH',
        body: input,
        baseUrl: NOTICE_BASE,
      });
      return { ...input, id, publishedAt: input.publishedAt, createdAt: '', updatedAt: '' } as AdminChangelog;
    },
    delete: async (id: string): Promise<void> => {
      await request<void>('/api/v1/notice/admin/changelogs/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
    publish: async (id: string): Promise<AdminChangelog> => {
      await request<void>('/api/v1/notice/admin/changelogs/' + id + '/publish', {
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
        '/api/v1/notice/admin/notifications',
        { baseUrl: NOTICE_BASE },
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      const mapped = items.map((n) => mapNotification(n as unknown as Record<string, unknown>));
      return mapped.sort((a, b) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0));
    },
    send: async (input: AdminNotificationInput): Promise<AdminNotification> => {
      const res = await request<{ id: string; accepted: boolean; queuedAt: string }>(
        '/api/v1/notice/admin/notifications/send',
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
      await request<void>('/api/v1/notice/admin/notifications/' + id, {
        method: 'DELETE',
        baseUrl: NOTICE_BASE,
      });
    },
  },

  // ---- Plans (Billing) ----
  plans: {
    list: async (): Promise<AdminPlan[]> => {
      const res = await request<{ plans: AdminPlan[]; items: AdminPlan[] } | AdminPlan[]>(
        '/api/v1/admin/plans',
        { baseUrl: ADMIN_BASE },
      );
      const items = Array.isArray(res) ? res : (res.plans ?? res.items ?? []);
      return items.map((p) => mapPlan(p as unknown as Record<string, unknown>));
    },
    create: async (input: AdminPlanInput): Promise<AdminPlan> => {
      return request<AdminPlan>('/api/v1/admin/plans', {
        method: 'POST',
        body: {
          name: input.name,
          plan_type: input.planType,
          price: input.price,
          category: input.category,
          monthly_limit: input.monthlyLimit,
          token_limit: input.tokenLimit,
          allowed_models: input.allowedModels,
          status: input.status,
        },
        baseUrl: ADMIN_BASE,
      });
    },
    update: async (id: string, input: AdminPlanInput): Promise<AdminPlan> => {
      return request<AdminPlan>('/api/v1/admin/plans/' + id, {
        method: 'PATCH',
        body: {
          name: input.name,
          plan_type: input.planType,
          price: input.price,
          category: input.category,
          monthly_limit: input.monthlyLimit,
          token_limit: input.tokenLimit,
          allowed_models: input.allowedModels,
          status: input.status,
        },
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
      const res = await request<{ plans: Record<string, unknown>[]; items: Record<string, unknown>[] } | Record<string, unknown>[]>(
        '/api/v1/admin/user-plans',
        { baseUrl: ADMIN_BASE },
      );
      const raw = Array.isArray(res) ? res : (res.plans ?? res.items ?? []);
      return raw.map(mapUserPlan);
    },
    assign: async (input: AdminUserPlanInput): Promise<AdminUserPlan> => {
      return request<AdminUserPlan>('/api/v1/admin/user-plans', {
        method: 'POST',
        body: {
          user_id: input.userId,
          plan_id: Number(input.planId),
          expires_at: input.expiresAt ?? undefined,
        },
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
    thinkingDefaultEffort: m.thinking_default_effort != null ? String(m.thinking_default_effort) : (m.thinkingDefaultEffort != null ? String(m.thinkingDefaultEffort) : null),
    thinkingMaxEffort: m.thinking_max_effort != null ? String(m.thinking_max_effort) : (m.thinkingMaxEffort != null ? String(m.thinkingMaxEffort) : null),
    thinkingMinBudgetToken: m.thinking_min_budget_token != null ? Number(m.thinking_min_budget_token) : (m.thinkingMinBudgetToken != null ? Number(m.thinkingMinBudgetToken) : null),
    thinkingMaxBudgetToken: m.thinking_max_budget_token != null ? Number(m.thinking_max_budget_token) : (m.thinkingMaxBudgetToken != null ? Number(m.thinkingMaxBudgetToken) : null),
    contextWindow: m.context_window != null ? Number(m.context_window) : (m.contextWindow != null ? Number(m.contextWindow) : null),
    maxOutputTokens: m.max_output_tokens != null ? Number(m.max_output_tokens) : (m.maxOutputTokens != null ? Number(m.maxOutputTokens) : null),
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
    contextWindow: r.context_window != null ? Number(r.context_window) : (r.contextWindow != null ? Number(r.contextWindow) : null),
    maxOutputTokens: r.max_output_tokens != null ? Number(r.max_output_tokens) : (r.maxOutputTokens != null ? Number(r.maxOutputTokens) : null),
    retryPolicy: mapRetryPolicy(r.retry_policy ?? r.retryPolicy),
  };
}

function mapRetryPolicy(v: unknown): RetryPolicy | null {
  if (!v || typeof v !== 'object') return null;
  const r = v as Record<string, unknown>;
  const rules = Array.isArray(r.rules) || Array.isArray(r.Rules)
    ? ((r.rules ?? r.Rules) as Record<string, unknown>[])
        .map((rr: Record<string, unknown>) => ({
          id: String(rr.id ?? rr.ID ?? ''),
          priority: Number(rr.priority ?? rr.Priority ?? 0),
          httpStatuses: Array.isArray(rr.http_statuses) || Array.isArray(rr.httpStatuses) || Array.isArray(rr.HTTPStatuses)
            ? ((rr.http_statuses ?? rr.httpStatuses ?? rr.HTTPStatuses) as number[]).map(Number)
            : [],
          errorCodes: Array.isArray(rr.error_codes) || Array.isArray(rr.errorCodes) || Array.isArray(rr.ErrorCodes)
            ? ((rr.error_codes ?? rr.errorCodes ?? rr.ErrorCodes) as string[]).map(String)
            : undefined,
          errorTypes: Array.isArray(rr.error_types) || Array.isArray(rr.errorTypes) || Array.isArray(rr.ErrorTypes)
            ? ((rr.error_types ?? rr.errorTypes ?? rr.ErrorTypes) as string[]).map(String)
            : undefined,
          action: String(rr.action ?? rr.Action ?? 'none') as RetryAction,
        }))
        .filter((rr: RetryRule) => rr.id !== '')
    : undefined;
  return {
    maxTotalAttempts: r.max_total_attempts != null ? Number(r.max_total_attempts) : (r.maxTotalAttempts != null ? Number(r.maxTotalAttempts) : (r.MaxTotalAttempts != null ? Number(r.MaxTotalAttempts) : null)),
    maxSameTargetAttempts: r.max_same_target_attempts != null ? Number(r.max_same_target_attempts) : (r.maxSameTargetAttempts != null ? Number(r.maxSameTargetAttempts) : (r.MaxSameTargetAttempts != null ? Number(r.MaxSameTargetAttempts) : null)),
    maxTotalDuration: typeof r.max_total_duration === 'string' ? r.max_total_duration : (typeof r.maxTotalDuration === 'string' ? r.maxTotalDuration : (typeof r.MaxTotalDuration === 'string' ? r.MaxTotalDuration : undefined)),
    backoff: typeof r.backoff === 'string' ? r.backoff : (typeof r.Backoff === 'string' ? r.Backoff : undefined),
    rules,
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

function mapEndpoint(e: Record<string, unknown>): AdminUpstreamEndpoint {
  return {
    id: Number(e.id ?? 0),
    providerId: String(e.provider_id ?? e.providerId ?? ''),
    path: String(e.path ?? ''),
    protocol: String(e.protocol ?? ''),
    authKind: (e.auth_kind ?? e.authKind ?? 'bearer_header') as AdminUpstreamEndpoint['authKind'],
    authHeader: e.auth_header != null ? String(e.auth_header) : (e.authHeader != null ? String(e.authHeader) : null),
    authQuery: e.auth_query != null ? String(e.auth_query) : (e.authQuery != null ? String(e.authQuery) : null),
    authPrefix: e.auth_prefix != null ? String(e.auth_prefix) : (e.authPrefix != null ? String(e.authPrefix) : null),
    status: (e.status ?? 'active') as AdminUpstreamEndpoint['status'],
    createdAt: String(e.created_at ?? e.createdAt ?? ''),
    updatedAt: String(e.updated_at ?? e.updatedAt ?? ''),
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
  createModel: async (input: { id: string; displayName: string; capabilities?: string[]; thinkingSupported?: boolean; thinkingDefaultEffort?: string | null; thinkingMaxEffort?: string | null; contextWindow?: number | null; maxOutputTokens?: number | null }): Promise<AdminModelConfig> => {
    return request<AdminModelConfig>('/api/v1/admin/models', {
      method: 'POST',
      body: {
        id: input.id,
        display_name: input.displayName,
        capabilities: input.capabilities ?? ['text'],
        thinking_supported: input.thinkingSupported ?? false,
        thinking_default_effort: input.thinkingDefaultEffort ?? null,
        thinking_max_effort: input.thinkingMaxEffort ?? null,
        context_window: input.contextWindow ?? null,
        max_output_tokens: input.maxOutputTokens ?? null,
        status: 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateModel: async (id: string, input: Partial<AdminModelConfig>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.displayName !== undefined) fields.display_name = input.displayName;
    if (input.thinkingSupported !== undefined) fields.thinking_supported = input.thinkingSupported;
    if (input.thinkingDefaultEffort !== undefined) fields.thinking_default_effort = input.thinkingDefaultEffort;
    if (input.thinkingMaxEffort !== undefined) fields.thinking_max_effort = input.thinkingMaxEffort;
    if (input.capabilities !== undefined) fields.capabilities = input.capabilities;
    if (input.contextWindow !== undefined) fields.context_window = input.contextWindow;
    if (input.maxOutputTokens !== undefined) fields.max_output_tokens = input.maxOutputTokens;
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
  createRoute: async (input: { id: string; modelId: string; providerId: string; upstreamModel: string; protocol: string; priority?: number; contextWindow?: number | null; maxOutputTokens?: number | null }): Promise<AdminRouteConfig> => {
    return request<AdminRouteConfig>('/api/v1/admin/routes', {
      method: 'POST',
      body: {
        id: input.id,
        model_id: input.modelId,
        provider_id: input.providerId,
        upstream_model: input.upstreamModel,
        protocol: input.protocol,
        priority: input.priority ?? 0,
        context_window: input.contextWindow ?? null,
        max_output_tokens: input.maxOutputTokens ?? null,
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
    if (input.contextWindow !== undefined) fields.context_window = input.contextWindow;
    if (input.maxOutputTokens !== undefined) fields.max_output_tokens = input.maxOutputTokens;
    if (input.retryPolicy !== undefined) fields.retry_policy = input.retryPolicy;
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

  // ---- Upstream Endpoints (端点) ----
  listEndpoints: async (providerId: string): Promise<AdminUpstreamEndpoint[]> => {
    const res = await request<{ items: AdminUpstreamEndpoint[] } | AdminUpstreamEndpoint[]>(
      `/api/v1/admin/providers/${providerId}/endpoints`,
      { baseUrl: ADMIN_BASE },
    );
    const items = Array.isArray(res) ? res : (res.items ?? []);
    return items.map((e) => mapEndpoint(e as unknown as Record<string, unknown>));
  },
  createEndpoint: async (providerId: string, input: {
    path: string;
    protocol: string;
    authKind: AdminUpstreamEndpoint['authKind'];
    authHeader?: string | null;
    authQuery?: string | null;
    authPrefix?: string | null;
    status?: string;
  }): Promise<AdminUpstreamEndpoint> => {
    return request<AdminUpstreamEndpoint>(`/api/v1/admin/providers/${providerId}/endpoints`, {
      method: 'POST',
      body: {
        path: input.path,
        protocol: input.protocol,
        auth_kind: input.authKind,
        auth_header: input.authHeader ?? null,
        auth_query: input.authQuery ?? null,
        auth_prefix: input.authPrefix ?? null,
        status: input.status ?? 'active',
      },
      baseUrl: ADMIN_BASE,
    });
  },
  updateEndpoint: async (id: number | string, input: Partial<AdminUpstreamEndpoint>): Promise<void> => {
    const fields: Record<string, unknown> = {};
    if (input.path !== undefined) fields.path = input.path;
    if (input.protocol !== undefined) fields.protocol = input.protocol;
    if (input.authKind !== undefined) fields.auth_kind = input.authKind;
    if (input.authHeader !== undefined) fields.auth_header = input.authHeader;
    if (input.authQuery !== undefined) fields.auth_query = input.authQuery;
    if (input.authPrefix !== undefined) fields.auth_prefix = input.authPrefix;
    if (input.status !== undefined) fields.status = input.status;
    await request<void>(`/api/v1/admin/endpoints/${id}`, {
      method: 'PATCH',
      body: fields,
      baseUrl: ADMIN_BASE,
    });
  },
  deleteEndpoint: async (id: number | string): Promise<void> => {
    await request<void>(`/api/v1/admin/endpoints/${id}`, {
      method: 'DELETE',
      baseUrl: ADMIN_BASE,
    });
  },

  // ---- Global policy (retry/timeout/auto_model_ids) ----
  getGlobalPolicy: async (): Promise<AdminGlobalPolicy> => {
    const res = await request<Record<string, unknown>>('/api/v1/admin/global', { baseUrl: ADMIN_BASE });
    return {
      default_retry: mapRetryPolicy(res.default_retry),
      default_timeout: (res.default_timeout as AdminGlobalPolicy['default_timeout']) ?? null,
      auto_model_ids: (res.auto_model_ids as string[] | null) ?? null,
    };
  },
  setGlobalRetry: async (policy: RetryPolicy): Promise<void> => {
    // Backend wire uses PascalCase keys (wireRetryPolicy). Convert from the
    // camelCase TS type so compile's json.Unmarshal into wireRetryPolicy works.
    const wire: Record<string, unknown> = {};
    if (policy.maxTotalAttempts != null) wire.MaxTotalAttempts = policy.maxTotalAttempts;
    if (policy.maxSameTargetAttempts != null) wire.MaxSameTargetAttempts = policy.maxSameTargetAttempts;
    if (policy.maxTotalDuration) wire.MaxTotalDuration = policy.maxTotalDuration;
    if (policy.backoff) wire.Backoff = policy.backoff;
    if (policy.rules) {
      wire.Rules = policy.rules.map((r) => ({
        ID: r.id,
        Priority: r.priority,
        HTTPStatuses: r.httpStatuses,
        ErrorCodes: r.errorCodes ?? [],
        ErrorTypes: r.errorTypes ?? [],
        Action: r.action,
      }));
    }
    await request<{ key: string }>(`/api/v1/admin/global/default_retry`, {
      method: 'PUT',
      body: wire,
      baseUrl: ADMIN_BASE,
    });
  },
  setGlobalTimeout: async (policy: NonNullable<AdminGlobalPolicy['default_timeout']>): Promise<void> => {
    await request<{ key: string }>(`/api/v1/admin/global/default_timeout`, {
      method: 'PUT',
      body: policy,
      baseUrl: ADMIN_BASE,
    });
  },
  // Set the global auto_model_ids list (ordered list of model IDs eligible
  // for the reserved "auto" selector). Pass null/empty to reset to the
  // default all-active-models-sorted fallback.
  setAutoModelIds: async (ids: string[] | null): Promise<void> => {
    await request<{ key: string }>(`/api/v1/admin/global/auto_model_ids`, {
      method: 'PUT',
      body: ids ?? [],
      baseUrl: ADMIN_BASE,
    });
  },
  compile: async (): Promise<{ revision: string }> => {
    const res = await request<{ revision: string; published: boolean }>(
      '/api/v1/admin/compile',
      { method: 'POST', baseUrl: ADMIN_BASE },
    );
    return { revision: res.revision };
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

// Admin domain types. Aligned to the admin OpenAPI contract (to be defined in
// packages/contracts/openapi/admin/v1.yaml). Mock-backed by default until the
// Edge admin endpoints land; components are agnostic to the source.

import type { ApiKey, RequestLog, RequestLogAttempt, UserPlan } from '@/types';

// ---- Users ----

export interface AdminUser {
  id: string;
  email: string;
  role: 'user' | 'admin';
  status: 'active' | 'disabled';
  createdAt: string;
}

export interface AdminUserDetail extends AdminUser {
  apiKeys: ApiKey[];
  userPlans: UserPlan[];
  recentRequests: RequestLog[];
  totalRequests: number;
}

export interface AdminUserListResponse {
  users: AdminUser[];
  total: number;
  page: number;
  pageSize: number;
}

// ---- API Keys (global, cross-user) ----

export interface AdminApiKey extends ApiKey {
  userEmail: string;
}

// ---- Request logs (global, cross-user) ----

export interface AdminRequestLog extends RequestLog {
  userId: string | null;
  userEmail: string | null;
  provider?: string | null;
  protocol?: string | null;
  stream?: boolean | null;
  httpStatus?: number | null;
  errorCode?: string | null;
  errorType?: string | null;
  errorMessage?: string | null;
  totalTokens?: number | null;
  billingPlan?: string | null;
  attempts?: RequestLogAttempt[];
}

export interface AdminRequestLogListResponse {
  logs: AdminRequestLog[];
  total: number;
  page: number;
  pageSize: number;
}

// ---- Dashboard stats ----

export interface TrendPoint {
  date: string; // YYYY-MM-DD
  requests: number;
  success: number;
  inputTokens: number;
  outputTokens: number;
}

export interface ModelUsageRow {
  model: string;
  requests: number;
  success: number;
  tokens: number;
}

export interface TopUserRow {
  email: string;
  requests: number;
  inputTokens: number;
  outputTokens: number;
  tokens: number;
}

export interface AdminDashboardStats {
  totalUsers: number;
  totalRequests: number;
  todayRequests: number;
  todaySuccess: number;
  todayActiveUsers: number;
  todayTokens: number;
  successRate: number;
  trend: TrendPoint[]; // 15-day trend
  todayModelUsage: ModelUsageRow[];
  todayTopUsers: TopUserRow[];
}

// ---- Plans (admin CRUD reuses the user-facing Plan shape) ----

export type { Plan, UserPlan } from '@/types';

// ---- Announcements (admin CRUD) ----

export interface AdminAnnouncement {
  id: string;
  title: string;
  summary: string;
  body: string; // Markdown
  severity: 'info' | 'warning' | 'maintenance';
  publishedAt: string | null; // null = draft
  createdAt: string;
  updatedAt: string;
}

export interface AdminAnnouncementInput {
  title: string;
  summary: string;
  body: string;
  severity: 'info' | 'warning' | 'maintenance';
  publishedAt: string | null;
}

// ---- Changelogs (admin CRUD) ----

export interface AdminChangelog {
  id: string;
  version: string;
  title: string;
  body: string; // Markdown
  publishedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface AdminChangelogInput {
  version: string;
  title: string;
  body: string;
  publishedAt: string | null;
}

// ---- Notifications (admin send + list) ----

export interface AdminNotification {
  id: string;
  userId: string;
  type: string;
  title: string;
  body: string;
  action: { type: string; label: string; href?: string } | null;
  readAt: string | null;
  createdAt: string;
}

export interface AdminNotificationInput {
  userId?: string; // empty = all users
  type: string;
  title: string;
  body: string;
  action: { type: string; label: string; href?: string } | null;
}

// ---- Plans admin (CRUD) ----

export interface AdminPlan {
  id: string;
  name: string;
  planType: 'coding' | 'token' | 'image' | 'free';
  price: number;
  category: 'monthly' | 'quarterly' | 'yearly';
  monthlyLimit: number | null;
  tokenLimit: number | null;
  allowedModels: string[];
  status: 'active' | 'disabled' | 'deleted';
  createdAt: string;
  updatedAt: string;
}

export interface AdminPlanInput {
  name: string;
  planType: 'coding' | 'token' | 'image' | 'free';
  price: number;
  category: 'monthly' | 'quarterly' | 'yearly';
  monthlyLimit: number | null;
  tokenLimit: number | null;
  allowedModels: string[];
  status: 'active' | 'disabled';
}

// ---- User plans admin (assignment) ----

export interface AdminUserPlan {
  id: string;
  userId: string;
  userEmail: string;
  planId: string;
  planName: string;
  planType: 'coding' | 'token' | 'image' | 'free';
  status: 'active' | 'expired' | 'cancelled';
  activatedAt: string;
  expiresAt: string | null;
  remainingQuota: string;
}

export interface AdminUserPlanInput {
  userId: string;
  planId: string;
  expiresAt: string | null;
}

// ---- Provider config (read-only for now, write TBD via Config Service) ----

export interface AdminProvider {
  id: string;
  name: string;
  displayLabel: string;
  selector: string;
  baseURL: string;
  sdkKind: 'openai' | 'anthropic';
  protocol: string;
  status: 'active' | 'disabled' | 'deleted';
  credentialCount: number;
  routeCount: number;
}

export interface AdminModelConfig {
  id: string;
  displayName: string;
  capabilities: string[]; // text|tools|vision|thinking|image
  thinkingSupported: boolean;
  contextWindow: number | null;
  maxOutputTokens: number | null;
  routeCount: number;
}

export interface AdminRouteConfig {
  id: string;
  modelId: string;
  providerId: string;
  upstreamModel: string;
  protocol: string;
  priority: number;
  enabled: boolean;
  quarantined: boolean;
  contextWindow: number | null;
  maxOutputTokens: number | null;
  retryPolicy?: RetryPolicy | null;
}

// ---- Upstream credentials (上游账号) ----

export interface AdminUpstreamCredential {
  id: string;
  providerId: string;
  credentialRef: string; // auto-generated vault:// ref
  apiKey?: string | null; // plaintext, only on create/update, never returned in list
  keyPrefix: string | null;
  keySuffix: string | null;
  priority: number;
  maxConcurrency: number | null;
  dailyQuota: number | null;
  status: 'active' | 'disabled' | 'deleted';
  createdAt: string;
  updatedAt: string;
}

// ---- Upstream endpoints (上游端点) ----

export interface AdminUpstreamEndpoint {
  id: number;
  providerId: string;
  path: string;
  protocol: string;
  authKind: 'bearer_header' | 'api_key_header' | 'api_key_query';
  authHeader: string | null;
  authQuery: string | null;
  authPrefix: string | null;
  status: 'active' | 'disabled' | 'deleted';
  createdAt: string;
  updatedAt: string;
}

// ---- Retry policy (全局 + 路由级) ----

/** 重试动作：匹配上游错误后如何选择下一个候选 */
export type RetryAction =
  | 'none'
  | 'same_credential' // 同目标重试（适用 503 瞬时过载）
  | 'next_credential' // 换同路由下另一个密钥（适用 429 限流）
  | 'next_route' // 换同模型另一路由（适用 5xx 上游故障）
  | 'next_provider' // 换 provider
  | 'next_model'; // 换模型

export interface RetryRule {
  id: string;
  priority: number;
  httpStatuses: number[];
  errorCodes?: string[];
  errorTypes?: string[];
  action: RetryAction;
}

export interface RetryPolicy {
  maxTotalAttempts?: number | null;
  maxSameTargetAttempts?: number | null;
  maxTotalDuration?: string; // e.g. "45s"
  backoff?: string; // e.g. "500ms"
  rules?: RetryRule[];
}

export interface TimeoutPolicy {
  requestTimeout?: string;
  ttftTimeout?: string;
  streamIdleTimeout?: string;
  streamMaxLifetime?: string;
  retryBackoff?: string;
}

/** 全局策略：GET /v1/config/admin/global 返回 */
export interface AdminGlobalPolicy {
  default_retry?: RetryPolicy | null;
  default_timeout?: TimeoutPolicy | null;
  auto_model_ids?: string[] | null;
}

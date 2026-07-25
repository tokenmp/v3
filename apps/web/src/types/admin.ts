// Admin domain types. Aligned to the admin OpenAPI contract (to be defined in
// packages/contracts/openapi/admin/v1.yaml). Mock-backed by default until the
// Edge admin endpoints land; components are agnostic to the source.

import type { ApiKey, RequestLog, UserPlan } from '@/types';

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
  tokens: number;
  cost: string;
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
  severity: 'info' | 'warning' | 'success';
  publishedAt: string | null; // null = draft
  createdAt: string;
  updatedAt: string;
}

export interface AdminAnnouncementInput {
  title: string;
  summary: string;
  body: string;
  severity: 'info' | 'warning' | 'success';
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

/** Types derived from @tokenmp/contracts OpenAPI auth v1. */

export type UserRole = 'user' | 'admin';
export type UserStatus = 'active' | 'disabled';

export interface User {
  id: string;
  email: string;
  role: UserRole;
  status: UserStatus;
  created_at: string;
}

export interface TokenResponse {
  access_token: string;
  refresh_token: string;
  token_type: 'Bearer';
  expires_in: number;
}

export type ErrorCode =
  | 'invalid_credentials'
  | 'email_taken'
  | 'invalid_token'
  | 'invalid_refresh_token'
  | 'password_too_weak'
  | 'invalid_email'
  | 'bad_request'
  | 'unauthorized'
  | 'internal_error'
  // Edge/BFF business endpoints (services/api)
  | 'billing_unavailable'
  | 'logging_unavailable'
  | 'auth_unavailable'
  | 'auth_error'
  | 'quota_unavailable'
  | 'not_found'
  | 'missing_request_id'
  | 'invalid_json'
  | 'invalid_preferred_billing'
  | 'forbidden'
  | 'service_unavailable';

/** Error envelope. Two wire shapes are supported:
 *  - contract: `{error:{code,message}}` (keys handler / Auth)
 *  - simplified: `{error:"code"}` (panel handlers in services/api) */
export interface ApiErrorBody {
  error: { code: ErrorCode; message: string } | string;
}

// ---- Business domain types (aligned to packages/contracts/openapi/api/v1.yaml). ----
// camelCase matches the contract field names exactly. The mock layer returns
// the same shape so swapping to the real API is transparent to components.

export type ApiKeyStatus = 'active' | 'disabled' | 'revoked';

export interface ApiKey {
  id: string;
  name: string;
  /** Display prefix, e.g. "tmp_abcd". */
  keyPrefix: string;
  /** Display suffix, e.g. "wxyz". */
  keySuffix: string;
  status: ApiKeyStatus;
  lastUsedAt: string | null;
  expiresAt: string | null;
  createdAt: string;
}

/** Returned only once on create/rotate; carries the full secret. */
export interface ApiKeyCreated extends ApiKey {
  /** Full secret, shown once. e.g. "tmp_<32 chars>". */
  secret: string;
}

export interface CreateKeyInput {
  name?: string;
  expiresAt?: string;
}

export interface UpdateKeyInput {
  name?: string;
  status?: ApiKeyStatus;
}

export type PlanType = 'coding' | 'token';
export type PlanStatus = 'active' | 'disabled';

export interface Plan {
  id: string;
  name: string;
  planType: PlanType;
  price: number;
  durationDays: number;
  /** Decimal quota as a string to preserve precision. */
  totalQuota: string;
  allowedModels: string[];
  status: PlanStatus;
}

export type UserPlanStatus = 'active' | 'expired' | 'disabled';

export interface UserPlan {
  id: string;
  planId: string;
  planName?: string | null;
  planType: PlanType;
  category?: string | null;
  price?: number | null;
  hourlyLimit?: number | null;
  weeklyLimit?: number | null;
  monthlyLimit?: number | null;
  tokenLimit?: string | null;
  totalQuota: string;
  remainingQuota: string;
  priority: number | null;
  status: UserPlanStatus;
  activatedAt: string;
  expiresAt: string | null;
}

/** Decimal amounts as strings to preserve precision. */
export interface UserBalance {
  codingRemaining: string;
  tokenRemaining: string;
}

export type RequestLogStatus = 'success' | 'error' | 'processing' | 'cancelled';

export interface RequestLog {
  requestId: string;
  model: string;
  status: RequestLogStatus;
  inputTokens: number | null;
  outputTokens: number | null;
  totalTokens?: number | null;
  cacheTokens?: number | null;
  /** Decimal cost as a string. */
  cost: string | null;
  durationMs: number | null;
  ttftMs?: number | null;
  protocol?: string | null;
  stream?: boolean | null;
  thinkingEffort?: string | null;
  thinkingRequestedEffort?: string | null;
  thinkingEffectiveEffort?: string | null;
  thinkingRequestedBudget?: number | null;
  thinkingEffectiveBudget?: number | null;
  thinkingEffortDegraded?: boolean | null;
  createdAt: string;
  completedAt?: string | null;
}

export interface RequestLogDetail extends RequestLog {
  provider: string;
  errorMessage: string | null;
  attempts: RequestLogAttempt[];
}

export interface RequestLogAttempt {
  // The contract leaves attempt item fields open; the backend fills them.
  // Kept as a loose record so the detail view can render whatever is present.
  [key: string]: unknown;
}

export interface RequestLogEvent {
  // The contract leaves event item fields open; the backend fills them.
  // Kept as a loose record so the detail view can render whatever is present.
  [key: string]: unknown;
}

export interface UsageStatsByModel {
  model: string;
  requests: number;
  inputTokens: string;
  outputTokens: string;
  cost: string;
}

export interface UsageStats {
  days: number;
  totalRequests: number;
  totalInputTokens: string;
  totalOutputTokens: string;
  totalCost: string;
  byModel: UsageStatsByModel[];
}

export type PreferredBilling = 'coding' | 'token';

export interface UserSettings {
  preferredBilling: PreferredBilling;
  fallbackEnabled: boolean;
}

export interface UserSettingsUpdate {
  preferredBilling?: PreferredBilling;
  fallbackEnabled?: boolean;
}

// ---- Notice domain types (aligned to packages/contracts/openapi/notice/v1.yaml). ----

export type AnnouncementSeverity = 'info' | 'warning' | 'maintenance';

export interface Announcement {
  id: string;
  title: string;
  summary: string;
  body: string;
  severity: AnnouncementSeverity;
  published_at: string;
}

export interface Changelog {
  id: string;
  version: string;
  title: string;
  body: string;
  published_at: string;
}

/** Generic, data-driven notification action. null means informational-only. */
export interface NotificationAction {
  type: 'link';
  label: string;
  href: string;
}

export interface Notification {
  id: string;
  type: string;
  title: string;
  body: string;
  action: NotificationAction | null;
  read_at: string | null;
  created_at: string;
}

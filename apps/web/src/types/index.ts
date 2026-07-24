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
  | 'internal_error';

export interface ApiErrorBody {
  error: { code: ErrorCode; message: string };
}

// ---- Mock-domain types (Panel pages). Backend endpoints not yet implemented. ----

export interface ApiKey {
  id: string;
  name: string;
  /** Masked display, e.g. "sk-***abcd". */
  masked: string;
  /** Full key, only present once right after creation. */
  full_key?: string;
  created_at: string;
  last_used_at: string | null;
  status: 'active' | 'revoked';
}

export interface RequestLog {
  id: string;
  created_at: string;
  model: string;
  provider: string;
  status: number;
  duration_ms: number;
  tokens_input: number;
  tokens_output: number;
}

export interface QuotaSummary {
  plan_name: string;
  used_tokens: number;
  total_tokens: number;
  reserved_tokens: number;
  expires_at: string | null;
}

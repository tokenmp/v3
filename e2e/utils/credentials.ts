type PlaywrightTest = {
  skip: (condition: boolean, description?: string) => void;
};

export type E2ECredentials = {
  baseURL?: string;
  user: { email: string; password: string };
  admin: { email: string; password: string };
  apiKey?: string;
  userId?: string;
  planId?: string;
};

const mockFallback = {
  user: { email: 'test-user@invalid', password: 'mock-user-password' },
  admin: { email: 'test-admin@invalid', password: 'mock-admin-password' },
};

/**
 * Resolves E2E identities exclusively from the process environment.
 *
 * Fallbacks are deliberately invalid mock/local placeholders. They keep the
 * helpers usable without secrets but must never authenticate against a live
 * target. Explicit BASE_URL runs must supply the matching E2E_* credentials.
 */
export function e2eCredentials(): E2ECredentials {
  return {
    baseURL: process.env.E2E_BASE_URL,
    user: {
      email: process.env.E2E_USER_EMAIL ?? mockFallback.user.email,
      password: process.env.E2E_USER_PASSWORD ?? mockFallback.user.password,
    },
    admin: {
      email: process.env.E2E_ADMIN_EMAIL ?? mockFallback.admin.email,
      password: process.env.E2E_ADMIN_PASSWORD ?? mockFallback.admin.password,
    },
    apiKey: process.env.E2E_API_KEY,
    userId: process.env.E2E_USER_ID,
    planId: process.env.E2E_PLAN_ID,
  };
}

export function hasAdminCreds(): boolean {
  return Boolean(process.env.E2E_ADMIN_EMAIL && process.env.E2E_ADMIN_PASSWORD);
}

export function hasUserCreds(): boolean {
  // E2E_BASE_URL opts into the global disposable-user pool. Retain the
  // provisioned-pair path for read-only BASE_URL investigations.
  return Boolean(
    process.env.E2E_BASE_URL
    || (process.env.E2E_USER_EMAIL && process.env.E2E_USER_PASSWORD),
  );
}

export function skipAdminIfNoCreds(test: PlaywrightTest): void {
  test.skip(
    !hasAdminCreds(),
    'Admin live tests require protected E2E_ADMIN_EMAIL and E2E_ADMIN_PASSWORD.',
  );
}

export function skipUserIfNoCreds(test: PlaywrightTest): void {
  test.skip(
    !hasUserCreds(),
    'User live tests require protected E2E_USER_EMAIL and E2E_USER_PASSWORD.',
  );
}

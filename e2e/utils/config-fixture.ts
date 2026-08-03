/**
 * E2E disposable Config fixture.
 *
 * Creates uniquely-suffixed provider/model/route rows so that write tests
 * (create / update / delete / compile) can run against a live backend without
 * mutating shared executor configuration. Every resource is tagged with an
 * `e2e-{runId}` prefix and cleaned up in afterEach regardless of test outcome.
 *
 * Requires E2E_BASE_URL + E2E_ADMIN credentials (same as all live admin tests).
 */
import { expect, type APIRequestContext } from '@playwright/test';
import { e2eCredentials } from './credentials';

export interface ConfigFixture {
  runId: string;
  providerId: string;
  modelId: string;
  routeId: string;
  cleanup: () => Promise<void>;
}

function adminHeaders(): Record<string, string> {
  const c = e2eCredentials();
  return {
    'content-type': 'application/json',
    // The Edge admin endpoints authenticate via JWT cookie set at login.
    // For API-context calls we pass the admin credentials explicitly.
  };
}

/**
 * Creates a disposable config fixture via the admin REST API. Each call mints
 * a new runId so parallel test workers never collide.
 */
export async function createConfigFixture(
  request: APIRequestContext,
  cookies: Record<string, string>,
): Promise<ConfigFixture> {
  const c = e2eCredentials();
  const base = c.baseURL!;
  const runId = `e2e${Date.now()}${Math.floor(Math.random() * 10000)}`;
  const providerId = `e2e-prov-${runId}`;
  const modelId = `e2e-model-${runId}`;
  const routeId = `e2e-route-${runId}`;

  const cookieHeader = Object.entries(cookies)
    .map(([k, v]) => `${k}=${v}`)
    .join('; ');
  const headers = { ...adminHeaders(), cookie: cookieHeader };

  // Create provider.
  const provRes = await request.post(`${base}/api/v1/admin/providers`, {
    data: {
      id: providerId,
      name: `E2E Provider ${runId}`,
      display_label: `E2E ${runId}`,
      selector: providerId,
      base_url: 'https://httpbin.org/status/200',
      sdk_kind: 'openai',
      protocol: 'openai_chat',
      status: 'active',
    },
    headers,
  });
  expect(provRes.ok(), `provider create failed: ${provRes.status()}`).toBeTruthy();

  // Create model.
  const modelRes = await request.post(`${base}/api/v1/admin/models`, {
    data: {
      id: modelId,
      display_name: `E2E Model ${runId}`,
      capabilities: ['chat'],
      thinking_supported: false,
      status: 'active',
    },
    headers,
  });
  expect(modelRes.ok(), `model create failed: ${modelRes.status()}`).toBeTruthy();

  // Create route.
  const routeRes = await request.post(`${base}/api/v1/admin/routes`, {
    data: {
      id: routeId,
      model_id: modelId,
      provider_id: providerId,
      upstream_model: 'gpt-4o-mini',
      protocol: 'openai_chat',
      priority: 999, // low priority so it never wins in production routing
      enabled: false, // disabled so it never serves real traffic
      status: 'active',
    },
    headers,
  });
  expect(routeRes.ok(), `route create failed: ${routeRes.status()}`).toBeTruthy();

  return {
    runId,
    providerId,
    modelId,
    routeId,
    async cleanup() {
      // Best-effort cleanup: delete route → model → provider. Ignore errors
      // since the test may have already deleted some of them.
      const cookieHeader = Object.entries(cookies)
        .map(([k, v]) => `${k}=${v}`)
        .join('; ');
      const headers = { ...adminHeaders(), cookie: cookieHeader };
      await request.delete(`${base}/api/v1/admin/routes/${routeId}`, { headers }).catch(() => {});
      await request.delete(`${base}/api/v1/admin/models/${modelId}`, { headers }).catch(() => {});
      await request.delete(`${base}/api/v1/admin/providers/${providerId}`, { headers }).catch(() => {});
    },
  };
}

/**
 * Extracts cookies from a browser context for use in APIRequestContext calls.
 */
export async function getCookiesFromContext(
  context: { cookies: () => Promise<Array<{ name: string; value: string }>> },
): Promise<Record<string, string>> {
  const cookies = await context.cookies();
  const result: Record<string, string> = {};
  for (const c of cookies) {
    result[c.name] = c.value;
  }
  return result;
}

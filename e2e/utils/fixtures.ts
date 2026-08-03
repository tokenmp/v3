import { test as base, expect, type TestInfo } from '@playwright/test';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { request as httpRequest } from 'node:http';
import { request as httpsRequest } from 'node:https';
import { dirname, join } from 'node:path';

export type DisposableUser = {
  id: string;
  email: string;
  password: string;
  accessToken: string;
};

type DisposableFixtureState = {
  users: DisposableUser[];
};

const fixtureFile = process.env.E2E_DISPOSABLE_FIXTURE_FILE
  ?? join(process.cwd(), 'test-results', '.disposable-fixtures.json');
// Match Playwright's BASE_URL precedence while using E2E_BASE_URL only as the
// explicit opt-in switch for disposable live fixtures.
const configuredBaseURL = process.env.BASE_URL ?? process.env.E2E_BASE_URL;

/** True only for an explicitly opted-in live target. */
export function usesDisposableFixtures(): boolean {
  return Boolean(configuredBaseURL);
}

function requiredFixtureState(): Promise<DisposableFixtureState> {
  if (!usesDisposableFixtures()) {
    throw new Error('Disposable fixtures are available only when E2E_BASE_URL is set.');
  }
  return readFile(fixtureFile, 'utf8').then((contents) => JSON.parse(contents) as DisposableFixtureState);
}

function userForWorker(state: DisposableFixtureState, testInfo: TestInfo): DisposableUser {
  if (state.users.length === 0) {
    throw new Error('Disposable fixture pool is empty.');
  }
  // A worker runs one test at a time. parallelIndex is unique across the
  // concurrent project workers, unlike workerIndex which restarts per project.
  return state.users[testInfo.parallelIndex % state.users.length];
}

type Fixtures = {
  /** Test-scoped access to a live user that is isolated from other workers. */
  disposableUser: DisposableUser;
  disposableToken: string;
};

export const test = base.extend<Fixtures>({
  disposableUser: async ({}, use, testInfo) => {
    if (!usesDisposableFixtures()) {
      const email = process.env.E2E_USER_EMAIL;
      const password = process.env.E2E_USER_PASSWORD;
      if (!email || !password) {
        throw new Error('Live user fixtures require E2E_BASE_URL or E2E_USER_EMAIL/E2E_USER_PASSWORD.');
      }
      await use({ id: process.env.E2E_USER_ID ?? '', email, password, accessToken: '' });
      return;
    }
    const state = await requiredFixtureState();
    await use(userForWorker(state, testInfo));
  },
  disposableToken: async ({ disposableUser }, use) => {
    await use(disposableUser.accessToken);
  },
});

export { expect };

function fixtureCount(): number {
  const value = Number(process.env.E2E_DISPOSABLE_USER_COUNT ?? 24);
  if (!Number.isInteger(value) || value < 2 || value > 64) {
    throw new Error('E2E_DISPOSABLE_USER_COUNT must be an integer from 2 through 64.');
  }
  return value;
}

function fixturePassword(): string {
  // Meets Auth's 12–128 rune, no-control-character policy. It is generated
  // per run and only written to the ignored Playwright result directory.
  return `E2e!${crypto.randomUUID().replaceAll('-', '')}Aa`;
}

type JSONResponse = {
  status: number;
  retryAfter?: string;
  body: unknown;
};

async function postJSON(url: string, body: unknown): Promise<JSONResponse> {
  const target = new URL(url);
  const data = JSON.stringify(body);
  const request = target.protocol === 'https:' ? httpsRequest : httpRequest;
  return new Promise((resolve, reject) => {
    const req = request(target, {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'content-length': Buffer.byteLength(data) },
      // This mirrors Playwright's ignoreHTTPSErrors setting for the explicitly
      // opted-in controlled dev target; browser tests remain unchanged.
      rejectUnauthorized: false,
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on('data', (chunk: Buffer) => chunks.push(chunk));
      response.on('end', () => {
        try {
          resolve({
            status: response.statusCode ?? 0,
            retryAfter: response.headers['retry-after'],
            body: JSON.parse(Buffer.concat(chunks).toString('utf8')),
          });
        } catch (error) {
          reject(new Error('Disposable fixture endpoint returned invalid JSON.', { cause: error }));
        }
      });
    });
    req.on('error', reject);
    req.end(data);
  });
}

async function retryAfter(response: JSONResponse): Promise<void> {
  const seconds = Number(response.retryAfter ?? 1);
  await new Promise((resolve) => setTimeout(resolve, Math.max(1, seconds) * 1000));
}

function unwrapData<T>(body: unknown): T {
  if (body && typeof body === 'object' && 'data' in body) {
    return (body as { data: T }).data;
  }
  return body as T;
}

async function createUser(baseURL: string, index: number, runID: string): Promise<DisposableUser> {
  const email = `e2e-fix-${runID}-${index}@disposable.test`;
  const password = fixturePassword();

  for (let attempt = 0; attempt < 4; attempt += 1) {
    const register = await postJSON(new URL('/api/v1/auth/register', baseURL).toString(), { email, password });
    if (register.status === 201) {
      const registered = unwrapData<{ id?: string }>(register.body);
      const login = await postJSON(new URL('/api/v1/auth/login', baseURL).toString(), { email, password });
      if (login.status !== 200) {
        throw new Error(`Disposable fixture login failed with HTTP ${login.status}.`);
      }
      const token = unwrapData<{ access_token?: string; accessToken?: string }>(login.body);
      const accessToken = token.access_token ?? token.accessToken;
      if (!registered.id || !accessToken) {
        throw new Error('Disposable fixture Auth response omitted the required public user ID or access token.');
      }
      return { id: registered.id, email, password, accessToken };
    }
    if (register.status === 429 && attempt < 3) {
      await retryAfter(register);
      continue;
    }
    throw new Error(`Disposable fixture registration failed with HTTP ${register.status}.`);
  }
  throw new Error('Disposable fixture registration exceeded retry attempts.');
}

/**
 * Playwright global setup for live E2E only. Auth registration deliberately
 * does not log a user in, so every registered identity is separately logged in
 * and its token is made available to fixture consumers.
 */
export default async function globalSetup(): Promise<() => Promise<void>> {
  if (!usesDisposableFixtures()) {
    return async () => undefined;
  }

  const baseURL = configuredBaseURL!;
  const runID = `${Date.now().toString(36)}-${crypto.randomUUID().slice(0, 8)}`;
  const users: DisposableUser[] = [];
  for (let index = 0; index < fixtureCount(); index += 1) {
    users.push(await createUser(baseURL, index, runID));
  }

  await mkdir(dirname(fixtureFile), { recursive: true });
  await writeFile(fixtureFile, JSON.stringify({ users }), { mode: 0o600 });

  return async () => {
    // Auth has no admin delete endpoint. Created users are intentionally kept
    // as disposable test records; remove local passwords/tokens after the run.
    await rm(fixtureFile, { force: true });
  };
}

/** Mock user-panel API. Backend endpoints not yet implemented.
 *  Mirrors the shape of real API calls so swapping to fetch later is trivial. */

import type { ApiKey, QuotaSummary, RequestLog } from '@/types';

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

const keys: ApiKey[] = [
  {
    id: 'key_1',
    name: '默认密钥',
    masked: 'sk-***a1b2',
    created_at: '2026-07-20T03:14:00Z',
    last_used_at: '2026-07-24T01:22:00Z',
    status: 'active',
  },
  {
    id: 'key_2',
    name: '测试环境',
    masked: 'sk-***c3d4',
    created_at: '2026-07-18T09:02:00Z',
    last_used_at: '2026-07-22T18:40:00Z',
    status: 'active',
  },
];

const models = ['gpt-4o', 'claude-3-5-sonnet', 'gpt-4o-mini', 'deepseek-chat', 'glm-4.5'];
const providers = ['openai', 'anthropic', 'deepseek', 'zhipu'];

function genRequests(count: number): RequestLog[] {
  const out: RequestLog[] = [];
  const now = Date.now();
  for (let i = 0; i < count; i++) {
    const status = i % 7 === 0 ? (i % 14 === 0 ? 500 : 429) : 200;
    out.push({
      id: `req_${(now - i * 1000).toString(36)}_${i}`,
      created_at: new Date(now - i * 1000 * 60 * 3).toISOString(),
      model: models[i % models.length]!,
      provider: providers[i % providers.length]!,
      status,
      duration_ms: 400 + ((i * 137) % 1800),
      tokens_input: 50 + ((i * 31) % 400),
      tokens_output: 80 + ((i * 53) % 600),
    });
  }
  return out;
}

const allRequests = genRequests(47);

export const userApi = {
  getQuota: async (): Promise<QuotaSummary> => {
    await delay(280);
    return {
      plan_name: '入门套餐',
      used_tokens: 128_400,
      total_tokens: 500_000,
      reserved_tokens: 3_200,
      expires_at: '2026-08-24T00:00:00Z',
    };
  },

  getRecentRequests: async (limit = 5): Promise<RequestLog[]> => {
    await delay(220);
    return allRequests.slice(0, limit);
  },

  getRequests: async (page = 1, pageSize = 10): Promise<{ items: RequestLog[]; total: number }> => {
    await delay(300);
    const start = (page - 1) * pageSize;
    return {
      items: allRequests.slice(start, start + pageSize),
      total: allRequests.length,
    };
  },

  getKeys: async (): Promise<ApiKey[]> => {
    await delay(260);
    return keys.filter((k) => k.status === 'active');
  },

  createKey: async (name: string): Promise<ApiKey> => {
    await delay(400);
    const full = 'sk-' + Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
    const masked = `sk-***${full.slice(-4)}`;
    const key: ApiKey = {
      id: `key_${Date.now()}`,
      name: name || '未命名密钥',
      masked,
      full_key: full,
      created_at: new Date().toISOString(),
      last_used_at: null,
      status: 'active',
    };
    keys.unshift(key);
    return key;
  },

  revokeKey: async (id: string): Promise<void> => {
    await delay(350);
    const k = keys.find((x) => x.id === id);
    if (k) k.status = 'revoked';
  },
};

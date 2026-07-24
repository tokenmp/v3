/** Notice API: announcements, notifications, changelogs.
 *  Mirrors packages/contracts/openapi/notice/v1.yaml. When the Notice Service
 *  backend is reachable (NEXT_PUBLIC_USE_MOCK_NOTICE=0), calls hit the real
 *  API; otherwise a mock layer returns representative data so the UI is
 *  fully exercised without a backend. */

import type { Announcement, Changelog, Notification } from '@/types';
import { request } from './core';

const useMock = process.env.NEXT_PUBLIC_USE_MOCK_NOTICE !== '0';

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ---- Mock data ----

const announcements: Announcement[] = [
  {
    id: '00000000-0000-0000-0000-0000000000a1',
    title: 'v3.2.0 正式上线',
    summary: '新增 Responses API、流式输出与模型目录；配额系统升级为类型化计量。',
    body: '## v3.2.0\n\n- 新增 OpenAI Responses API（non-stream + stream）\n- 透传 Anthropic Messages 流式\n- `GET /v1/models` 返回快照驱动目录\n- 配额计量升级为类型化域\n\n请前往「版本日志」查看完整变更。',
    severity: 'info',
    published_at: '2026-07-22T08:00:00Z',
  },
  {
    id: '00000000-0000-0000-0000-0000000000a2',
    title: '7 月 26 日 02:00–04:00 计划维护',
    summary: '为升级配额存储进行计划维护，期间请求可能短暂失败。',
    body: '## 计划维护\n\n维护窗口：**2026-07-26 02:00–04:00 (UTC+8)**\n\n影响范围：执行层请求可能返回 503，重试策略自动生效。鉴权服务不受影响。',
    severity: 'maintenance',
    published_at: '2026-07-24T01:30:00Z',
  },
  {
    id: '00000000-0000-0000-0000-0000000000a3',
    title: 'Anthropic 上游限流提醒',
    summary: '上游 Anthropic 出现间歇 529，执行层已映射为 429 并启用退避。',
    body: '## 上游限流\n\nAnthropic 出现间歇性 overloaded（529），执行层已安全映射为 429，并按编译策略退避重试。如持续异常请关注公告更新。',
    severity: 'warning',
    published_at: '2026-07-21T16:20:00Z',
  },
];

const changelogs: Changelog[] = [
  {
    id: '00000000-0000-0000-0000-0000000000c1',
    version: 'v3.2.0',
    title: 'Responses API、流式与模型目录',
    body: '## 新功能\n\n- OpenAI Responses API（non-stream + stream）\n- Anthropic Messages 流式透传\n- 快照驱动模型目录 `GET /v1/models`\n\n## 改进\n\n- 配额计量升级为类型化域\n- 配置热重载（SIGHUP + 可选轮询）\n\n## 修复\n\n- Retry-After 头解析与回写',
    published_at: '2026-07-22T08:00:00Z',
  },
  {
    id: '00000000-0000-0000-0000-0000000000c2',
    version: 'v3.1.0',
    title: '认证身份流',
    body: '## 新功能\n\n- 注册 / 登录 / 刷新令牌轮换\n- Ed25519/EdDSA Access Token\n- Argon2id 密码哈希（兼容 bcrypt）\n- logout-all 与 token_version 失效',
    published_at: '2026-06-30T08:00:00Z',
  },
];

const notifications: Notification[] = [
  {
    id: '00000000-0000-0000-0000-0000000000n1',
    type: 'plan_activated',
    title: '您的「入门套餐」已启用',
    body: '入门套餐已激活，包含 500,000 tokens 月度额度，有效期至 2026-08-24。',
    action: {
      type: 'link',
      label: '查看套餐详情',
      href: '/panel/billing/plans/plan_starter',
    },
    read_at: null,
    created_at: '2026-07-24T02:00:00Z',
  },
  {
    id: '00000000-0000-0000-0000-0000000000n2',
    type: 'plan_disabled',
    title: '您的「试用套餐」已停用',
    body: '试用套餐因额度耗尽已自动停用。如需继续使用，请续费或升级。',
    action: {
      type: 'link',
      label: '升级套餐',
      href: '/panel/billing/plans',
    },
    read_at: null,
    created_at: '2026-07-23T09:10:00Z',
  },
  {
    id: '00000000-0000-0000-0000-0000000000n3',
    type: 'system',
    title: '欢迎使用 TokenMP',
    body: '您的账户已创建成功。建议先创建一个 API 密钥开始接入。',
    action: {
      type: 'link',
      label: '创建密钥',
      href: '/panel/keys',
    },
    read_at: '2026-07-20T03:20:00Z',
    created_at: '2026-07-20T03:14:00Z',
  },
];

async function realGet<T>(path: string): Promise<T> {
  return request<T>(path);
}

async function realPost(path: string): Promise<void> {
  await request<void>(path, { method: 'POST', noContent: true });
}

export const noticeApi = {
  // ---- Announcements ----
  listAnnouncements: async (limit = 20, offset = 0): Promise<{ items: Announcement[]; total: number }> => {
    if (useMock) {
      await delay(260);
      const start = offset;
      return { items: announcements.slice(start, start + limit), total: announcements.length };
    }
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/announcements?${qs}`);
  },

  getAnnouncement: async (id: string): Promise<Announcement> => {
    if (useMock) {
      await delay(200);
      const a = announcements.find((x) => x.id === id);
      if (!a) throw new Error('not_found');
      return a;
    }
    return realGet(`/api/v1/announcements/${id}`);
  },

  // ---- Changelogs ----
  listChangelogs: async (limit = 20, offset = 0): Promise<{ items: Changelog[]; total: number }> => {
    if (useMock) {
      await delay(240);
      const start = offset;
      return { items: changelogs.slice(start, start + limit), total: changelogs.length };
    }
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/changelogs?${qs}`);
  },

  getChangelog: async (id: string): Promise<Changelog> => {
    if (useMock) {
      await delay(180);
      const c = changelogs.find((x) => x.id === id);
      if (!c) throw new Error('not_found');
      return c;
    }
    return realGet(`/api/v1/changelogs/${id}`);
  },

  // ---- Notifications ----
  listNotifications: async (limit = 20, offset = 0): Promise<{ items: Notification[]; total: number }> => {
    if (useMock) {
      await delay(260);
      const start = offset;
      return { items: notifications.slice(start, start + limit), total: notifications.length };
    }
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/notifications?${qs}`);
  },

  unreadCount: async (): Promise<number> => {
    if (useMock) {
      await delay(120);
      return notifications.filter((n) => n.read_at === null).length;
    }
    const r = await realGet<{ count: number }>(`/api/v1/notifications/unread-count`);
    return r.count;
  },

  markRead: async (id: string): Promise<void> => {
    if (useMock) {
      await delay(140);
      const n = notifications.find((x) => x.id === id);
      if (n) n.read_at = new Date().toISOString();
      return;
    }
    return realPost(`/api/v1/notifications/${id}/read`);
  },

  markAllRead: async (): Promise<void> => {
    if (useMock) {
      await delay(180);
      const now = new Date().toISOString();
      notifications.forEach((n) => {
        if (n.read_at === null) n.read_at = now;
      });
      return;
    }
    return realPost('/api/v1/notifications/read-all');
  },
};

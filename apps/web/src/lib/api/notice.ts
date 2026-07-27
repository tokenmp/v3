/** Notice API: announcements, notifications, changelogs.
 *  All methods call the real Notice Service via NOTICE_BASE. */

import type { Announcement, Changelog, Notification } from '@/types';
import { request, NOTICE_BASE } from './core';

async function realGet<T>(path: string): Promise<T> {
  return request<T>(path, { baseUrl: NOTICE_BASE });
}

async function realPost(path: string): Promise<void> {
  await request<void>(path, { method: 'POST', noContent: true, baseUrl: NOTICE_BASE });
}

export const noticeApi = {
  // ---- Announcements ----
  listAnnouncements: async (limit = 20, offset = 0): Promise<{ items: Announcement[]; total: number }> => {
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/notice/announcements?${qs}`);
  },

  getAnnouncement: async (id: string): Promise<Announcement> => {
    return realGet(`/api/v1/notice/announcements/${id}`);
  },

  // ---- Changelogs ----
  listChangelogs: async (limit = 20, offset = 0): Promise<{ items: Changelog[]; total: number }> => {
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/notice/changelogs?${qs}`);
  },

  getChangelog: async (id: string): Promise<Changelog> => {
    return realGet(`/api/v1/notice/changelogs/${id}`);
  },

  // ---- Notifications ----
  listNotifications: async (limit = 20, offset = 0): Promise<{ items: Notification[]; total: number }> => {
    const qs = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    return realGet(`/api/v1/notice/notifications?${qs}`);
  },

  unreadCount: async (): Promise<number> => {
    const r = await realGet<{ count: number }>(`/api/v1/notice/notifications/unread-count`);
    return r.count;
  },

  markRead: async (id: string): Promise<void> => {
    return realPost(`/api/v1/notice/notifications/${id}/read`);
  },

  markAllRead: async (): Promise<void> => {
    return realPost('/api/v1/notice/notifications/read-all');
  },
};

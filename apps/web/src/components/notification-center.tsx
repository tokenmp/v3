'use client';

import { useEffect, useRef, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Bell, Megaphone, Check } from 'lucide-react';
import { noticeApi } from '@/lib/api/notice';
import { cn } from '@/lib/utils';

/**
 * NotificationCenter: a bell icon in the panel header that opens a popover
 * (tooltip-style) showing recent announcements and unread notifications.
 *
 * Two tabs: 公告 (announcements, newest-first) and 通知 (notifications). The
 * bell shows a badge with the unread notification count; opening the 通知
 * tab does not auto-mark-read (clicking an item or "全部已读" does).
 */
type Tab = 'announcements' | 'notifications';

function formatRelative(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const diff = Date.now() - t;
  const m = Math.floor(diff / 60000);
  if (m < 1) return '刚刚';
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  return new Date(iso).toLocaleDateString('zh-CN');
}

export function NotificationCenter() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<Tab>('announcements');
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  // Popover coordinates relative to the viewport, computed on open so the
  // panel aligns to the bell's right edge and never overflows on mobile.
  const [popStyle, setPopStyle] = useState<React.CSSProperties>({});

  const { data: announcements } = useQuery({
    queryKey: ['notice', 'announcements', 'header'] as const,
    queryFn: () => noticeApi.listAnnouncements(10, 0),
    enabled: open,
    staleTime: 30_000,
  });
  const annItems = announcements?.items ?? [];

  const { data: unread = 0 } = useQuery({
    queryKey: ['notice', 'unread-count'] as const,
    queryFn: noticeApi.unreadCount,
    staleTime: 60_000,
  });

  const { data: notificationsResp } = useQuery({
    queryKey: ['notice', 'notifications', 'header'] as const,
    queryFn: () => noticeApi.listNotifications(10, 0),
    enabled: open && tab === 'notifications',
    staleTime: 30_000,
  });
  const notifItems = notificationsResp?.items ?? [];

  const markAllRead = useMutation({
    mutationFn: () => noticeApi.markAllRead(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['notice'] });
    },
  });

  const markRead = useMutation({
    mutationFn: (id: string) => noticeApi.markRead(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['notice'] });
    },
  });

  // Close on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Position the popover: align its right edge to the bell's right edge,
  // clamped so it never overflows the viewport (mobile-friendly). Computed
  // synchronously on click (after open flips) so the bell is in its final
  // layout position; a resize listener keeps it aligned while open.
  const place = () => {
    const el = triggerRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const wantW = Math.min(384, window.innerWidth - 16);
    let left = r.right - wantW;
    if (left < 8) left = 8;
    setPopStyle({
      position: 'fixed',
      top: r.bottom + 6,
      left,
      width: wantW,
      maxWidth: window.innerWidth - 16,
    });
  };

  useEffect(() => {
    if (!open) return;
    // Re-measure on the next frame after the browser settles any layout
    // shift triggered by opening (e.g. scrollbar/mobile reflow).
    const raf = requestAnimationFrame(place);
    window.addEventListener('resize', place);
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener('resize', place);
    };
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        onClick={() => {
          setOpen((v) => {
            const next = !v;
            if (next) {
              // Compute position synchronously on open so the popover lands
              // correctly before paint.
              requestAnimationFrame(place);
            }
            return next;
          });
        }}
        className="focus-inset relative inline-flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
        aria-label="通知与公告"
      >
        <Bell className="h-4 w-4" />
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold text-destructive-foreground">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div
          role="dialog"
          aria-label="通知与公告"
          style={popStyle}
          className="z-50 overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-lg"
        >
          {/* Tabs */}
          <div className="flex border-b">
            {(
              [
                ['announcements', '公告', annItems.length],
                ['notifications', '通知', unread],
              ] as const
            ).map(([key, label, badge]) => (
              <button
                key={key}
                type="button"
                onClick={() => setTab(key)}
                className={cn(
                  'relative flex flex-1 items-center justify-center gap-1.5 px-3 py-2.5 text-sm font-medium transition-colors',
                  tab === key ? 'text-foreground' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {label}
                {badge > 0 && (
                  <span className="flex h-4 min-w-4 items-center justify-center rounded-full bg-muted px-1 text-[10px] font-semibold">
                    {badge > 99 ? '99+' : badge}
                  </span>
                )}
                {tab === key && (
                  <span className="absolute inset-x-3 bottom-0 h-0.5 rounded-full bg-primary" />
                )}
              </button>
            ))}
          </div>

          {/* Content */}
          <div className="max-h-[60vh] overflow-y-auto">
            {tab === 'announcements' ? (
              annItems.length === 0 ? (
                <Empty text="暂无公告" icon={<Megaphone className="h-5 w-5" />} />
              ) : (
                <ul className="divide-y">
                  {annItems.map((a) => (
                    <li key={a.id} className="px-4 py-3 hover:bg-accent/50 transition-colors">
                      <div className="flex items-start justify-between gap-2">
                        <span className="font-medium text-sm leading-snug">{a.title}</span>
                        <span className="shrink-0 text-[11px] text-muted-foreground">
                          {formatRelative(a.published_at)}
                        </span>
                      </div>
                      {a.summary && (
                        <p className="mt-1 text-xs text-muted-foreground line-clamp-2">{a.summary}</p>
                      )}
                    </li>
                  ))}
                </ul>
              )
            ) : notifItems.length === 0 ? (
              <Empty text="暂无通知" icon={<Bell className="h-5 w-5" />} />
            ) : (
              <ul className="divide-y">
                {notifItems.map((n) => {
                  const unreadItem = !n.read_at;
                  return (
                    <li
                      key={n.id}
                      className={cn(
                        'px-4 py-3 hover:bg-accent/50 transition-colors cursor-pointer',
                        unreadItem && 'bg-primary/5',
                      )}
                      onClick={() => {
                        if (unreadItem) markRead.mutate(n.id);
                      }}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <span className="font-medium text-sm leading-snug">{n.title}</span>
                        <span className="shrink-0 text-[11px] text-muted-foreground">
                          {formatRelative(n.created_at)}
                        </span>
                      </div>
                      {n.body && (
                        <p className="mt-1 text-xs text-muted-foreground line-clamp-2">{n.body}</p>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {/* Footer */}
          {tab === 'notifications' && notifItems.length > 0 && unread > 0 && (
            <div className="border-t p-2">
              <button
                type="button"
                onClick={() => markAllRead.mutate()}
                disabled={markAllRead.isPending}
                className="flex w-full items-center justify-center gap-1.5 rounded-md py-1.5 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
              >
                <Check className="h-3.5 w-3.5" />
                全部标为已读
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Empty({ text, icon }: { text: string; icon: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-10 text-muted-foreground">
      {icon}
      <span className="text-sm">{text}</span>
    </div>
  );
}

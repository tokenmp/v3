'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ChevronRight, ArrowLeft } from 'lucide-react';
import { adminMobileMoreGroups } from '@/lib/admin-nav';
import { useAuthStore } from '@/lib/auth';
import { Card, CardContent } from '@/components/ui/card';
import { cn } from '@/lib/utils';

const SERVICES = [
  { name: 'Edge/BFF', url: '/healthz' },
  { name: 'Auth', url: '/healthz' },
  { name: 'Notice', url: '/healthz' },
  { name: 'Executor', url: '/healthz' },
];

function ServiceStatusBadge({ name, url }: { name: string; url: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['healthz', name],
    queryFn: async () => {
      try {
        const res = await fetch(url);
        return { ok: res.ok };
      } catch {
        return { ok: false };
      }
    },
    refetchInterval: 30000,
  });

  const isUp = data?.ok === true;

  return (
    <div className="flex items-center justify-between">
      <span className="text-sm">{name}</span>
      <span
        className={cn(
          'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium',
          isLoading
            ? 'bg-muted text-muted-foreground'
            : isUp
              ? 'bg-green-100 text-green-700'
              : 'bg-red-100 text-red-700',
        )}
      >
        <span
          className={cn(
            'size-1.5 rounded-full',
            isLoading ? 'bg-muted-foreground' : isUp ? 'bg-green-500' : 'bg-red-500',
          )}
        />
        {isLoading ? '检查中…' : isUp ? '运行中' : '不可用'}
      </span>
    </div>
  );
}

/**
 * Admin mobile "更多" hub.
 *
 * On mobile the bottom nav's last tab is this page (mirrors /panel/settings).
 * It groups every admin section that is not a primary bottom tab into cards
 * of link rows, so the tab bar stays clean while every page remains one tap
 * away. The desktop sidebar (hidden md:flex) is unaffected and is the
 * canonical navigation on large screens.
 */
export default function AdminMorePage() {
  const user = useAuthStore((s) => s.user);
  const email = user?.email ?? '';
  const initial = email.charAt(0).toUpperCase() || '?';

  return (
    <div className="space-y-5 md:hidden">
      <h1 className="text-lg font-semibold px-1">更多</h1>

      {/* Account info card */}
      <Card>
        <CardContent className="flex items-center gap-4 p-5">
          <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xl font-semibold">
            {initial}
          </div>
          <div className="min-w-0 flex-1">
            <div className="font-semibold truncate">{email || '管理员'}</div>
            <div className="text-xs text-muted-foreground mt-0.5">管理员</div>
          </div>
          <Link
            href="/panel"
            className="flex items-center gap-1 rounded-md bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            用户面板
          </Link>
        </CardContent>
      </Card>

      {/* System status */}
      <div>
        <div className="mb-2 px-1 text-xs font-medium text-muted-foreground">系统状态</div>
        <Card>
          <CardContent className="divide-y p-0">
            {SERVICES.map((s) => (
              <div key={s.name} className="px-4 py-3">
                <ServiceStatusBadge name={s.name} url={s.url} />
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      {/* Navigation groups */}
      {adminMobileMoreGroups.map((group) => (
        <div key={group.label}>
          <div className="mb-2 px-1 text-xs font-medium text-muted-foreground">
            {group.label}
          </div>
          <Card>
            <CardContent className="divide-y p-0">
              {group.items.map((item) => {
                const Icon = item.icon;
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    className="flex items-center gap-3 px-4 py-3.5 text-sm hover:bg-accent transition-colors"
                  >
                    <Icon className="h-4 w-4 text-muted-foreground" />
                    <span className="flex-1 text-left">{item.label}</span>
                    <ChevronRight className="h-4 w-4 text-muted-foreground" />
                  </Link>
                );
              })}
            </CardContent>
          </Card>
        </div>
      ))}
    </div>
  );
}

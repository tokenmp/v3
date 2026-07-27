'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { mobileTabs } from '@/lib/nav';
import { cn } from '@/lib/utils';

/**
 * Mobile bottom tab bar. 4 tabs: 概览 / 请求日志 / 模型 / 我的. The "我的" tab
 * is the settings hub that hosts notice entries (announcements/notifications/
 * changelogs), model config, and account actions, so the tab bar stays clean
 * and notice features are one tap away rather than behind a "更多" sheet.
 */
export function BottomNav() {
  const pathname = usePathname();

  const isActive = (href: string) => {
    if (href === '/panel') return pathname === '/panel';
    // The "我的" tab should stay active on all settings-subroutes too.
    if (href === '/panel/settings') {
      return pathname.startsWith('/panel/settings')
        || pathname.startsWith('/panel/announcements')
        || pathname.startsWith('/panel/notifications')
        || pathname.startsWith('/panel/changelogs')
        || pathname.startsWith('/panel/auto-model');
    }
    return pathname.startsWith(href);
  };

  return (
    <nav className="md:hidden fixed bottom-0 inset-x-0 border-t bg-card h-16 flex items-center justify-around z-40 pb-[env(safe-area-inset-bottom)]">
      {mobileTabs.map((item) => {
        const Icon = item.icon;
        const active = isActive(item.href);
        return (
          <Link
            key={item.href}
            href={item.href}
            className={cn(
              'flex flex-col items-center gap-0.5 text-xs transition-colors focus-inset rounded-md px-3 py-1',
              active ? 'text-primary' : 'text-muted-foreground',
            )}
          >
            <Icon className="h-5 w-5" />
            <span>{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}

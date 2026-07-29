'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { adminMobileTabs } from '@/lib/admin-nav';
import { cn } from '@/lib/utils';

/**
 * Admin mobile bottom tab bar — mirrors the panel's BottomNav so the two
 * apps feel consistent on mobile. Five primary destinations (概览/用户/日志/
 * 执行/更多); the \"更多\" tab is the /admin/more hub that groups every other
 * section as link rows (like /panel/settings). The desktop sidebar is
 * unaffected (hidden md:flex in AdminLayout).
 */
export function AdminBottomNav() {
  const pathname = usePathname();

  const isActive = (href: string) => {
    if (href === '/admin') return pathname === '/admin';
    // \"更多\" stays active on its own page only; sub-routes belong to other tabs.
    if (href === '/admin/more') return pathname === '/admin/more';
    // \"执行\" tab should stay active on all execution sub-routes too.
    if (href === '/admin/models') {
      return pathname.startsWith('/admin/models')
        || pathname.startsWith('/admin/providers')
        || pathname.startsWith('/admin/credentials')
        || pathname.startsWith('/admin/routes')
        || pathname.startsWith('/admin/retry')
        || pathname.startsWith('/admin/auto-model');
    }
    // \"用户\" tab stays active on user-domain sub-routes too.
    if (href === '/admin/users') {
      return pathname.startsWith('/admin/users')
        || pathname.startsWith('/admin/api-keys')
        || pathname.startsWith('/admin/plans');
    }
    return pathname.startsWith(href);
  };

  return (
    <nav className="md:hidden fixed bottom-0 inset-x-0 border-t bg-card h-16 flex items-center justify-around z-40 pb-[env(safe-area-inset-bottom)]">
      {adminMobileTabs.map((item) => {
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

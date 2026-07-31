'use client';

import { ShieldCheck } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useAuthGuard } from '@/hooks/use-auth-guard';
import { AppLayout } from '@/components/app-layout';
import { AppHeader } from '@/components/app-header';
import { AppSidebar } from '@/components/app-sidebar';
import { AppBottomNav } from '@/components/app-bottom-nav';
import { navGroups, mobileTabs } from '@/lib/nav';
import { useAuthStore } from '@/lib/auth';
import { NotificationCenter } from '@/components/notification-center';
import { DropdownMenuItem } from '@/components/ui/dropdown-menu';
import { noticeApi } from '@/lib/api/notice';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';

/** Resolve the current page label from the panel nav config based on the pathname. */
function findPanelLabel(pathname: string): string {
  for (const group of navGroups) {
    for (const item of group.items) {
      if (item.href === pathname) return item.label;
      if (item.href !== '/panel' && pathname.startsWith(item.href + '/')) return item.label;
    }
  }
  return '';
}

function panelIsActive(href: string, pathname: string) {
  if (href === '/panel') return pathname === '/panel';
  if (href === '/panel/settings') {
    return (
      pathname.startsWith('/panel/settings') ||
      pathname.startsWith('/panel/announcements') ||
      pathname.startsWith('/panel/notifications') ||
      pathname.startsWith('/panel/changelogs') ||
      pathname.startsWith('/panel/auto-model')
    );
  }
  return pathname.startsWith(href);
}

function PanelSidebarLogo() {
  const { data: changelogs } = useQuery({
    queryKey: ['notice', 'changelogs', 'version'] as const,
    queryFn: () => noticeApi.listChangelogs(1, 0),
    staleTime: 5 * 60_000,
  });
  const latestVersion = changelogs?.items?.[0]?.version;

  return (
    <div className="flex flex-col leading-none">
      <span className="font-semibold text-lg">TokenMP</span>
      {latestVersion && (
        <Link
          href="/panel/changelogs"
          className="text-[10px] font-normal text-muted-foreground hover:text-foreground transition-colors"
          onClick={(e) => e.stopPropagation()}
        >
          {latestVersion}
        </Link>
      )}
    </div>
  );
}

function PanelHeader() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);

  return (
    <AppHeader
      breadcrumbRoot="TokenMP"
      findLabel={findPanelLabel}
      extraActions={
        <div className="hidden md:block">
          <NotificationCenter />
        </div>
      }
      extraMenuItems={
        user?.role === 'admin' ? (
          <DropdownMenuItem onClick={() => router.push('/admin')}>
            <ShieldCheck className="h-4 w-4" />
            管理后台
          </DropdownMenuItem>
        ) : null
      }
    />
  );
}

export default function PanelLayout({ children }: { children: React.ReactNode }) {
  const { isHydrated, isAuthenticated } = useAuthGuard();

  return (
    <AppLayout
      isHydrated={isHydrated}
      isAuthorized={isAuthenticated}
      sidebar={
        <AppSidebar
          navGroups={navGroups}
          logoHref="/panel"
          logoAriaLabel="TokenMP"
          variant="panel"
          logoContent={<PanelSidebarLogo />}
        />
      }
      header={<PanelHeader />}
      bottomNav={<AppBottomNav items={mobileTabs} isActive={panelIsActive} />}
    >
      {children}
    </AppLayout>
  );
}

'use client';

import { ArrowLeft } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useAdminGuard } from '@/hooks/use-admin-guard';
import { AppLayout } from '@/components/app-layout';
import { AppHeader } from '@/components/app-header';
import { AppSidebar } from '@/components/app-sidebar';
import { AppBottomNav } from '@/components/app-bottom-nav';
import { adminNavGroups, adminMobileTabs, findAdminLabel } from '@/lib/admin-nav';

function adminIsActive(href: string, pathname: string) {
  if (href === '/admin') return pathname === '/admin';
  if (href === '/admin/more') return pathname === '/admin/more';
  if (href === '/admin/models') {
    return (
      pathname.startsWith('/admin/models') ||
      pathname.startsWith('/admin/providers') ||
      pathname.startsWith('/admin/credentials') ||
      pathname.startsWith('/admin/routes') ||
      pathname.startsWith('/admin/retry') ||
      pathname.startsWith('/admin/auto-model')
    );
  }
  if (href === '/admin/users') {
    return (
      pathname.startsWith('/admin/users') ||
      pathname.startsWith('/admin/api-keys') ||
      pathname.startsWith('/admin/plans')
    );
  }
  return pathname.startsWith(href);
}

function AdminSidebarLogo() {
  return <span className="font-semibold text-lg">Admin</span>;
}

function AdminHeaderActions() {
  const router = useRouter();
  return (
    <button
      type="button"
      onClick={() => router.push('/panel')}
      className="focus-inset hidden sm:inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-sm text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
      title="返回用户面板"
    >
      <ArrowLeft className="h-4 w-4" />
      用户面板
    </button>
  );
}

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { isHydrated, isAuthenticated, isAdmin } = useAdminGuard();

  return (
    <AppLayout
      isHydrated={isHydrated}
      isAuthorized={isAuthenticated && isAdmin}
      sidebar={
        <AppSidebar
          navGroups={adminNavGroups}
          logoHref="/admin"
          logoAriaLabel="TokenMP Admin"
          variant="admin"
          logoContent={<AdminSidebarLogo />}
        />
      }
      header={
        <AppHeader
          breadcrumbRoot="Admin"
          findLabel={(pathname) => findAdminLabel(pathname) ?? 'Admin'}
          extraActions={<AdminHeaderActions />}
        />
      }
      bottomNav={<AppBottomNav items={adminMobileTabs} isActive={adminIsActive} />}
    >
      {children}
    </AppLayout>
  );
}

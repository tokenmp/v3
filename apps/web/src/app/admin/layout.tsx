'use client';

import { useState, useEffect } from 'react';
import { useAdminGuard } from '@/hooks/use-admin-guard';
import { useSidebarStore } from '@/lib/sidebar-store';
import { AdminSidebar } from '@/components/admin-sidebar';
import { AdminHeader } from '@/components/admin-header';
import { AdminBottomNav } from '@/components/admin-bottom-nav';

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { isHydrated, isAuthenticated, isAdmin } = useAdminGuard();
  const collapsed = useSidebarStore((s) => s.collapsed);
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const isCollapsed = mounted && collapsed;

  if (!isHydrated) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <div className="flex flex-col items-center gap-3">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
          <span className="text-sm text-muted-foreground">加载中…</span>
        </div>
      </div>
    );
  }

  if (!isAuthenticated || !isAdmin) {
    return null;
  }

  return (
    <div className="min-h-dvh">
      <AdminSidebar />
      <div className={isCollapsed ? 'md:pl-[3.75rem]' : 'md:pl-60'}>
        <AdminHeader />
        <main className="p-4 sm:p-6 pb-24 md:pb-6">{children}</main>
      </div>
      <AdminBottomNav />
    </div>
  );
}

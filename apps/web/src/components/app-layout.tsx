'use client';

import { useState, useEffect, type ReactNode } from 'react';
import { useSidebarStore } from '@/lib/sidebar-store';
import { Spinner } from '@/components/ui/spinner';

export interface AppLayoutProps {
  /** Whether the auth state has been hydrated from storage. */
  isHydrated: boolean;
  /** Whether the user is authorized to see this layout. */
  isAuthorized: boolean;
  /** Sidebar component instance. */
  sidebar: ReactNode;
  /** Header component instance. */
  header: ReactNode;
  /** Bottom nav component instance. */
  bottomNav: ReactNode;
  /** Page content. */
  children: ReactNode;
}

export function AppLayout({
  isHydrated,
  isAuthorized,
  sidebar,
  header,
  bottomNav,
  children,
}: AppLayoutProps) {
  const collapsed = useSidebarStore((s) => s.collapsed);
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const isCollapsed = mounted && collapsed;

  if (!isHydrated) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (!isAuthorized) {
    return null;
  }

  return (
    <div className="min-h-dvh">
      {sidebar}
      <div className={isCollapsed ? 'md:pl-[3.75rem]' : 'md:pl-60'}>
        {header}
        <main className="p-4 sm:p-6 pb-24 md:pb-6">{children}</main>
      </div>
      {bottomNav}
    </div>
  );
}

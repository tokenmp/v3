'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';
import { useSidebarStore } from '@/lib/sidebar-store';
import { adminNavGroups } from '@/lib/admin-nav';
import { cn } from '@/lib/utils';

export function AdminSidebar() {
  const pathname = usePathname();
  const collapsed = useSidebarStore((s) => s.collapsed);
  const toggle = useSidebarStore((s) => s.toggle);

  // Avoid hydration mismatch: render expanded until mounted.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const isCollapsed = mounted && collapsed;

  const isActive = (href: string) => {
    if (href === '/admin') return pathname === '/admin';
    return pathname.startsWith(href);
  };

  return (
    <aside
      data-collapsed={isCollapsed}
      className={cn(
        'hidden md:flex md:flex-col md:fixed md:inset-y-0 border-r bg-sidebar transition-[width] duration-200',
        isCollapsed ? 'md:w-[3.75rem]' : 'md:w-60',
      )}
    >
      {/* Logo */}
      <div
        className={cn(
          'flex h-16 items-center border-b',
          isCollapsed ? 'justify-center px-0' : 'gap-2.5 px-5',
        )}
      >
        <Link href="/admin" aria-label="TokenMP Admin" className="flex items-center gap-2.5">
          <TokenMPLogoMark className="h-7 w-7 shrink-0" />
          {!isCollapsed && (
            <span className="font-semibold text-lg">Admin</span>
          )}
        </Link>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto overflow-x-hidden py-4">
        {adminNavGroups.map((group, gi) => (
          <div key={gi} className="px-3">
            {group.label && !isCollapsed && (
              <p className="mb-1 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                {group.label}
              </p>
            )}
            <ul className="space-y-1">
              {group.items.map((item) => {
                const Icon = item.icon;
                const active = isActive(item.href);
                return (
                  <li key={item.href}>
                    <Link
                      href={item.href}
                      title={isCollapsed ? item.label : undefined}
                      className={cn(
                        'group relative flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium transition-colors focus-nav',
                        active
                          ? 'bg-primary/10 text-primary border-l-2 border-primary'
                          : 'text-muted-foreground hover:bg-accent hover:text-foreground border-l-2 border-transparent',
                        isCollapsed && 'justify-center',
                      )}
                    >
                      <Icon className="h-4 w-4 shrink-0" />
                      {!isCollapsed && <span className="truncate">{item.label}</span>}
                      {isCollapsed && (
                        <span className="pointer-events-none absolute left-full ml-2 z-50 whitespace-nowrap rounded-md bg-popover px-2 py-1 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity group-hover:opacity-100">
                          {item.label}
                        </span>
                      )}
                    </Link>
                  </li>
                );
              })}
            </ul>
            {gi < adminNavGroups.length - 1 && <div className="my-3 border-t" />}
          </div>
        ))}
      </nav>

      {/* Collapse toggle */}
      <div className="border-t p-3">
        <button
          type="button"
          onClick={toggle}
          className="focus-nav group relative flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
        >
          {isCollapsed ? (
            <>
              <PanelLeftOpen className="h-4 w-4" />
              <span className="pointer-events-none absolute left-full ml-2 z-50 whitespace-nowrap rounded-md bg-popover px-2 py-1 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity group-hover:opacity-100">
                展开侧边栏
              </span>
            </>
          ) : (
            <>
              <PanelLeftClose className="h-4 w-4" />
              <span>收起侧边栏</span>
            </>
          )}
        </button>
      </div>
    </aside>
  );
}

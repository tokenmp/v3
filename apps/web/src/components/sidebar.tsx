'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';
import { useSidebarStore } from '@/lib/sidebar-store';
import { navGroups } from '@/lib/nav';
import { cn } from '@/lib/utils';

export function Sidebar() {
  const pathname = usePathname();
  const collapsed = useSidebarStore((s) => s.collapsed);
  const toggle = useSidebarStore((s) => s.toggle);

  // Avoid hydration mismatch: render expanded until mounted.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const isCollapsed = mounted && collapsed;

  const isActive = (href: string) => {
    if (href === '/panel') return pathname === '/panel';
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
        <Link href="/panel" aria-label="TokenMP" className="flex items-center gap-2.5">
          <TokenMPLogoMark className="h-7 w-7 shrink-0" />
          {!isCollapsed && <span className="font-semibold text-lg">TokenMP</span>}
        </Link>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto overflow-x-hidden py-4">
        {navGroups.map((group, gi) => (
          <div key={gi} className="mb-4">
            {group.label && !isCollapsed && (
              <div className="px-5 mb-2 text-xs uppercase text-muted-foreground tracking-wider">
                {group.label}
              </div>
            )}
            {group.items.map((item) => {
              const Icon = item.icon;
              const active = isActive(item.href);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  title={isCollapsed ? item.label : undefined}
                  className={cn(
                    'group relative flex items-center text-sm transition-colors focus-nav',
                    isCollapsed
                      ? 'justify-center mx-2 py-2.5 rounded-md'
                      : 'gap-3 px-5 py-2.5 border-l-2',
                    active
                      ? 'bg-primary/10 text-primary border-primary font-medium'
                      : 'text-muted-foreground hover:bg-accent hover:text-foreground border-transparent',
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  {!isCollapsed && <span>{item.label}</span>}
                  {isCollapsed && (
                    <span className="pointer-events-none absolute left-full ml-2 z-50 whitespace-nowrap rounded-md bg-popover px-2 py-1 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity group-hover:opacity-100">
                      {item.label}
                    </span>
                  )}
                </Link>
              );
            })}
          </div>
        ))}
      </nav>

      {/* Collapse toggle (sits above the user section) */}
      <div className="border-t">
        <button
          type="button"
          onClick={toggle}
          aria-label={isCollapsed ? '展开侧边栏' : '收起侧边栏'}
          className={cn(
            'group relative flex items-center text-sm text-muted-foreground hover:bg-accent hover:text-foreground transition-colors w-full focus-inset',
            isCollapsed
              ? 'justify-center py-3'
              : 'gap-3 px-5 py-2.5',
          )}
        >
          {isCollapsed ? (
            <>
              <PanelLeftOpen className="h-4 w-4 shrink-0" />
              <span className="pointer-events-none absolute left-full ml-2 z-50 whitespace-nowrap rounded-md bg-popover px-2 py-1 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity group-hover:opacity-100">
                展开侧边栏
              </span>
            </>
          ) : (
            <>
              <PanelLeftClose className="h-4 w-4 shrink-0" />
              <span>收起侧边栏</span>
            </>
          )}
        </button>
      </div>
    </aside>
  );
}

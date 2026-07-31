'use client';

import { useState, useEffect, type ReactNode } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';
import { useSidebarStore } from '@/lib/sidebar-store';
import type { NavGroup } from '@/lib/nav';
import { cn } from '@/lib/utils';

export interface AppSidebarProps {
  /** Navigation groups to render. */
  navGroups: NavGroup[];
  /** Logo link destination. */
  logoHref: string;
  /** Logo aria-label. */
  logoAriaLabel: string;
  /** Content rendered beside the logo mark when expanded (e.g. app name + version). */
  logoContent?: ReactNode;
  /** Visual variant: 'panel' uses flat links with border-l; 'admin' uses ul/li with rounded-md. */
  variant?: 'panel' | 'admin';
  /** Active path resolver. Defaults to exact match for root, prefix match for others. */
  isActive?: (href: string, pathname: string) => boolean;
}

const defaultIsActive = (href: string, pathname: string) => {
  // Exact match for root routes; prefix match for the rest.
  if (href === '/panel' || href === '/admin') return pathname === href;
  return pathname.startsWith(href);
};

export function AppSidebar({
  navGroups,
  logoHref,
  logoAriaLabel,
  logoContent,
  variant = 'panel',
  isActive = defaultIsActive,
}: AppSidebarProps) {
  const pathname = usePathname();
  const collapsed = useSidebarStore((s) => s.collapsed);
  const toggle = useSidebarStore((s) => s.toggle);

  // Avoid hydration mismatch: render expanded until mounted.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const isCollapsed = mounted && collapsed;

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
        <Link href={logoHref} aria-label={logoAriaLabel} className="flex items-center gap-2.5">
          <TokenMPLogoMark className="h-7 w-7 shrink-0" />
          {!isCollapsed && logoContent}
        </Link>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto overflow-x-hidden py-4">
        {variant === 'admin' ? (
          // Admin variant: ul/li with rounded-md items and group dividers
          navGroups.map((group, gi) => (
            <div key={gi} className="px-3">
              {group.label && !isCollapsed && (
                <p className="mb-1 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                  {group.label}
                </p>
              )}
              <ul className="space-y-1">
                {group.items.map((item) => {
                  const Icon = item.icon;
                  const active = isActive(item.href, pathname);
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
              {gi < navGroups.length - 1 && <div className="my-3 border-t" />}
            </div>
          ))
        ) : (
          // Panel variant: flat links with border-l-2
          navGroups.map((group, gi) => (
            <div key={gi} className="mb-4">
              {group.label && !isCollapsed && (
                <div className="px-5 mb-2 text-xs uppercase text-muted-foreground tracking-wider">
                  {group.label}
                </div>
              )}
              {group.items.map((item) => {
                const Icon = item.icon;
                const active = isActive(item.href, pathname);
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
          ))
        )}
      </nav>

      {/* Collapse toggle */}
      {variant === 'admin' ? (
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
      ) : (
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
      )}
    </aside>
  );
}

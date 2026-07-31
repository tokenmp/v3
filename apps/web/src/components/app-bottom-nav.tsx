'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { NavItem } from '@/lib/nav';
import { cn } from '@/lib/utils';

export interface AppBottomNavProps {
  /** Tab items to render. */
  items: NavItem[];
  /** Active state resolver: given an item href and the current pathname, return true if active. */
  isActive: (href: string, pathname: string) => boolean;
}

export function AppBottomNav({ items, isActive }: AppBottomNavProps) {
  const pathname = usePathname();

  return (
    <nav className="md:hidden fixed bottom-0 inset-x-0 border-t bg-card h-16 flex items-center justify-around z-40 pb-[env(safe-area-inset-bottom)]">
      {items.map((item) => {
        const Icon = item.icon;
        const active = isActive(item.href, pathname);
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

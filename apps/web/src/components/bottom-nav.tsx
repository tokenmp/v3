'use client';

import { useState } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { MoreHorizontal, X } from 'lucide-react';
import { mobileTabs, mobileMore } from '@/lib/nav';
import { cn } from '@/lib/utils';

export function BottomNav() {
  const pathname = usePathname();
  const [sheetOpen, setSheetOpen] = useState(false);

  const isActive = (href: string) => {
    if (href === '/panel') return pathname === '/panel';
    return pathname.startsWith(href);
  };

  return (
    <>
      <nav className="md:hidden fixed bottom-0 inset-x-0 border-t bg-card h-16 flex items-center justify-around z-40">
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
        <button
          type="button"
          onClick={() => setSheetOpen(true)}
          className={cn(
            'flex flex-col items-center gap-0.5 text-xs transition-colors focus-inset rounded-md px-3 py-1',
            mobileMore.some((item) => isActive(item.href))
              ? 'text-primary'
              : 'text-muted-foreground',
          )}
        >
          <MoreHorizontal className="h-5 w-5" />
          <span>更多</span>
        </button>
      </nav>

      {/* Bottom sheet overlay */}
      {sheetOpen && (
        <div className="md:hidden fixed inset-0 z-50" onClick={() => setSheetOpen(false)}>
          <div className="absolute inset-0 bg-black/50" />
          <div
            className="absolute bottom-0 inset-x-0 rounded-t-2xl bg-background p-4 pb-8 animate-in slide-in-from-bottom"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between mb-4">
              <span className="text-sm font-medium">更多</span>
              <button
                type="button"
                onClick={() => setSheetOpen(false)}
                className="focus-inset inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-accent"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-1">
              {mobileMore.map((item) => {
                const Icon = item.icon;
                const active = isActive(item.href);
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    onClick={() => setSheetOpen(false)}
                    className={cn(
                      'flex items-center gap-3 rounded-md px-3 py-3 text-sm transition-colors focus-inset',
                      active
                        ? 'bg-primary/10 text-primary font-medium'
                        : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                    )}
                  >
                    <Icon className="h-4 w-4" />
                    {item.label}
                  </Link>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </>
  );
}

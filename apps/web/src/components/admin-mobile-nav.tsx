'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { Menu, X } from 'lucide-react';
import { adminNavGroups } from '@/lib/admin-nav';
import { cn } from '@/lib/utils';

/**
 * Admin mobile navigation: a hamburger button in the admin header (mobile
 * only) that opens a left-side drawer listing every admin nav group. The
 * desktop sidebar (hidden md:flex) covers large screens; without this, mobile
 * users had no way to move between admin sections except editing the URL.
 */
export function AdminMobileNav() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  // Close on route change.
  useEffect(() => {
    setOpen(false);
  }, [pathname]);

  // Lock background scroll while the drawer is open.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  const isActive = (href: string) => {
    if (href === '/admin') return pathname === '/admin';
    return pathname.startsWith(href);
  };

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="focus-inset md:hidden inline-flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
        aria-label="打开管理后台导航"
      >
        <Menu className="h-5 w-5" />
      </button>

      {open && (
        <div className="md:hidden fixed inset-0 z-50" role="dialog" aria-label="管理后台导航">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setOpen(false)}
          />
          <div className="absolute inset-y-0 left-0 w-[min(80vw,18rem)] bg-background shadow-xl flex flex-col animate-in slide-in-from-left">
            <div className="flex h-16 items-center justify-between border-b px-4">
              <span className="font-semibold">管理后台</span>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="focus-inset inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-accent"
                aria-label="关闭导航"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <nav className="flex-1 overflow-y-auto py-2">
              {adminNavGroups.map((group, gi) => (
                <div key={gi} className="mb-3">
                  {group.label && (
                    <div className="px-4 mb-1 text-xs uppercase tracking-wider text-muted-foreground">
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
                        className={cn(
                          'flex items-center gap-3 px-4 py-2.5 text-sm transition-colors',
                          active
                            ? 'bg-primary/10 text-primary font-medium'
                            : 'text-foreground/80 hover:bg-accent',
                        )}
                      >
                        <Icon className="h-4 w-4" />
                        {item.label}
                      </Link>
                    );
                  })}
                </div>
              ))}
            </nav>
          </div>
        </div>
      )}
    </>
  );
}

'use client';

import Link from 'next/link';
import type { NotificationAction } from '@/types';

/** Data-driven notification action renderer.
 *  - null → render nothing
 *  - action.type === 'link' → render a Link styled as outline button
 *  - unknown type → render nothing (graceful ignore)
 */
function isSafeInternalHref(href: string): boolean {
  return href.startsWith('/')
    && !href.startsWith('//')
    && !href.includes('\\')
    && !/[\u0000-\u001F\u007F]/.test(href);
}

export function NotificationAction({ action }: { action: NotificationAction | null }) {
  if (!action) return null;
  if (action.type !== 'link' || !isSafeInternalHref(action.href)) return null;

  return (
    <Link
      href={action.href}
      className="focus-inset inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium border border-input bg-background hover:bg-accent hover:text-accent-foreground h-9 px-3 transition-colors"
    >
      {action.label}
    </Link>
  );
}

'use client';

import Link from 'next/link';
import { ChevronRight } from 'lucide-react';
import { adminMobileMoreGroups } from '@/lib/admin-nav';
import { Card, CardContent } from '@/components/ui/card';

/**
 * Admin mobile \"更多\" hub.
 *
 * On mobile the bottom nav's last tab is this page (mirrors /panel/settings).
 * It groups every admin section that is not a primary bottom tab into cards
 * of link rows, so the tab bar stays clean while every page remains one tap
 * away. The desktop sidebar (hidden md:flex) is unaffected and is the
 * canonical navigation on large screens.
 */
export default function AdminMorePage() {
  return (
    <div className="space-y-5 md:hidden">
      <h1 className="text-lg font-semibold px-1">更多</h1>
      {adminMobileMoreGroups.map((group) => (
        <div key={group.label}>
          <div className="mb-2 px-1 text-xs font-medium text-muted-foreground">
            {group.label}
          </div>
          <Card>
            <CardContent className="divide-y p-0">
              {group.items.map((item) => {
                const Icon = item.icon;
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    className="flex items-center gap-3 px-4 py-3.5 text-sm hover:bg-accent transition-colors"
                  >
                    <Icon className="h-4 w-4 text-muted-foreground" />
                    <span className="flex-1 text-left">{item.label}</span>
                    <ChevronRight className="h-4 w-4 text-muted-foreground" />
                  </Link>
                );
              })}
            </CardContent>
          </Card>
        </div>
      ))}
    </div>
  );
}

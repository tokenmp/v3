import Link from 'next/link';
import { AlertTriangle, Settings } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * Lightweight hint shown on config-edit pages after centralising snapshot
 * publication into /admin/settings. It keeps operators aware that DB edits do
 * not reach Executor until the unified publish action is triggered, without
 * putting another heavy button on every page (especially mobile).
 *
 * Note: edits (create/update/delete of models, providers, routes, accounts)
 * are saved to the DB immediately — they show up in the admin UI right away.
 * The unified publish only controls when the compiled snapshot reaches the
 * running Executor, so editing one entity never blocks editing another.
 */
export function PublishStatusHint({ className }: { className?: string }) {
  return (
    <Link
      href="/admin/settings"
      className={cn(
        'inline-flex items-center gap-1.5 rounded-md border bg-card px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground',
        className,
      )}
    >
      <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
      <span className="hidden sm:inline">编辑已即时保存，点这里发布生效</span>
      <span className="sm:hidden">去发布</span>
      <Settings className="h-3.5 w-3.5" />
    </Link>
  );
}

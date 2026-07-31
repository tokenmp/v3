import { cn } from '@/lib/utils';

/**
 * Consistent page header for admin pages.
 * Title is always text-xl font-semibold; description is text-sm text-muted-foreground.
 * Optional `actions` slot aligns to the right on desktop.
 */
export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between', className)}>
      <div>
        <h1 className="text-xl font-semibold">{title}</h1>
        {description ? (
          <p className="text-sm text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="mt-2 sm:mt-0">{actions}</div> : null}
    </div>
  );
}

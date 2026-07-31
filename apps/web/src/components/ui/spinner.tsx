import { cn } from '@/lib/utils';

interface SpinnerProps {
  className?: string;
  label?: string;
}

/** Reusable loading spinner. Extracted from duplicated layout loading states. */
export function Spinner({ className, label = '加载中…' }: SpinnerProps) {
  return (
    <div className={cn('flex flex-col items-center gap-3', className)}>
      <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
      {label && <span className="text-sm text-muted-foreground">{label}</span>}
    </div>
  );
}

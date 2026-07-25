import { cn } from '@/lib/utils';

/**
 * 通用筛选 chip（胶囊按钮）。
 * 用于列表页工具栏的筛选区。
 */
export function FilterChip({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex h-7 items-center rounded-full px-3 text-xs font-medium transition-colors',
        active ? 'bg-primary text-primary-foreground' : 'border border-border bg-card hover:bg-accent',
      )}
    >
      {label}
    </button>
  );
}

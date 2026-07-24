import { cn } from '@/lib/utils';

export function TokenMPLogoMark({ className }: { className?: string }) {
  return (
    <svg
      className={cn('h-7 w-7 shrink-0', className)}
      viewBox="0 0 64 64"
      fill="none"
      aria-hidden="true"
    >
      <rect width="64" height="64" rx="16" fill="var(--brand-solid)" />
      <rect x="11" y="33" width="20" height="20" rx="6" fill="white" opacity="0.96" />
      <rect x="33" y="11" width="20" height="20" rx="6" fill="white" opacity="0.62" />
      <circle cx="32" cy="32" r="11" fill="white" />
    </svg>
  );
}

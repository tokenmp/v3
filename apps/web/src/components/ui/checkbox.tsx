import * as React from 'react';
import { Check } from 'lucide-react';
import { cn } from '@/lib/utils';

const Checkbox = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & {
    checked?: boolean;
    onCheckedChange?: (checked: boolean) => void;
  }
>(({ className, checked, onCheckedChange, onClick, ...props }, ref) => (
  <button
    ref={ref}
    type="button"
    role="checkbox"
    aria-checked={checked}
    className={cn(
      'peer h-4 w-4 shrink-0 rounded-sm border border-primary ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
      checked && 'bg-primary text-primary-foreground',
      className,
    )}
    onClick={(e) => {
      const next = !checked;
      onCheckedChange?.(next);
      onClick?.(e);
    }}
    {...props}
  >
    {checked ? <Check className="h-3 w-3" /> : null}
  </button>
));
Checkbox.displayName = 'Checkbox';

export { Checkbox };

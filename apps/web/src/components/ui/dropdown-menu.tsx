'use client';

import * as React from 'react';
import { cn } from '@/lib/utils';

interface DropdownContextValue {
  open: boolean;
  setOpen: (v: boolean) => void;
  /** Trigger ref, refocused on close. */
  triggerRef: React.RefObject<HTMLButtonElement | null>;
  /** Register an item element for roving tabindex. */
  registerItem: (el: HTMLButtonElement | null) => void;
  /** Move focus among items by delta (-1/0/+1). */
  focusItem: (delta: number) => void;
  /** Focus the first item (called on open). */
  focusFirst: () => void;
}
const DropdownContext = React.createContext<DropdownContextValue | null>(null);

export function DropdownMenu({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = React.useState(false);
  const triggerRef = React.useRef<HTMLButtonElement | null>(null);
  const itemsRef = React.useRef<Array<HTMLButtonElement | null>>([]);

  const refreshItems = React.useCallback(() => {
    itemsRef.current = itemsRef.current.filter(Boolean);
  }, []);

  const registerItem = React.useCallback((el: HTMLButtonElement | null) => {
    if (el) {
      if (!itemsRef.current.includes(el)) itemsRef.current.push(el);
    } else {
      refreshItems();
    }
  }, [refreshItems]);

  const focusItem = React.useCallback((delta: number) => {
    let items = itemsRef.current.filter(Boolean) as HTMLButtonElement[];
    if (items.length === 0) {
      const content = document.querySelector('[role="menu"]');
      if (content) {
        items = Array.from(content.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
        itemsRef.current = items;
      }
    }
    if (items.length === 0) return;
    const current = document.activeElement;
    const idx = items.indexOf(current as HTMLButtonElement);
    const next = idx < 0 ? (delta > 0 ? 0 : items.length - 1) : (idx + delta + items.length) % items.length;
    items[next]?.focus();
  }, []);

  const focusFirst = React.useCallback(() => {
    let items = itemsRef.current.filter(Boolean) as HTMLButtonElement[];
    if (items.length === 0) {
      // Fallback: query the live DOM in case items haven't registered yet.
      const content = document.querySelector('[role="menu"]');
      if (content) {
        items = Array.from(content.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
        itemsRef.current = items;
      }
    }
    items[0]?.focus();
  }, []);

  const value = React.useMemo<DropdownContextValue>(
    () => ({ open, setOpen, triggerRef, registerItem, focusItem, focusFirst }),
    [open, registerItem, focusItem, focusFirst],
  );

  return (
    <DropdownContext.Provider value={value}>
      <div className="relative">{children}</div>
    </DropdownContext.Provider>
  );
}

export function DropdownMenuTrigger({
  children,
  asChild,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { asChild?: boolean }) {
  const ctx = React.useContext(DropdownContext)!;
  const ref = React.useRef<HTMLButtonElement | null>(null);

  // Keep context triggerRef synced.
  React.useEffect(() => {
    ctx.triggerRef.current = ref.current;
  });

  const handleKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      e.stopPropagation();
      if (!ctx.open) {
        ctx.setOpen(true);
      }
      // Focus the first menu item once rendered.
      requestAnimationFrame(() => {
        const first = document.querySelector<HTMLButtonElement>('[role="menuitem"]');
        first?.focus();
      });
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!ctx.open) {
        ctx.setOpen(true);
      }
      requestAnimationFrame(() => {
        const first = document.querySelector<HTMLButtonElement>('[role="menuitem"]');
        first?.focus();
      });
    }
    props.onKeyDown?.(e);
  };

  const handleClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    // e.detail === 0 means the click was triggered by keyboard (Enter/Space);
    // those are handled in handleKeyDown, so skip to avoid toggling closed.
    if (e.detail === 0) return;
    e.stopPropagation();
    ctx.setOpen(!ctx.open);
    props.onClick?.(e);
  };

  if (asChild) {
    return (
      <span
        ref={ref}
        role="button"
        tabIndex={0}
        onClick={handleClick}
        onKeyDown={handleKeyDown as unknown as React.KeyboardEventHandler<HTMLSpanElement>}
        className="inline-flex"
      >
        {children}
      </span>
    );
  }
  return (
    <button
      ref={ref}
      type="button"
      aria-haspopup="menu"
      aria-expanded={ctx.open}
      {...props}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
    >
      {children}
    </button>
  );
}

export function DropdownMenuContent({
  children,
  className,
  align = 'end',
}: {
  children: React.ReactNode;
  className?: string;
  align?: 'start' | 'end';
}) {
  const ctx = React.useContext(DropdownContext)!;
  const contentRef = React.useRef<HTMLDivElement | null>(null);

  React.useEffect(() => {
    if (!ctx.open) return;
    // Focus the first menu item when the menu opens, after items have registered.
    const t = setTimeout(() => ctx.focusFirst(), 0);
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        ctx.focusItem(1);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        ctx.focusItem(-1);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        ctx.setOpen(false);
        ctx.triggerRef.current?.focus();
      } else if (e.key === 'Tab') {
        // Trap focus inside the menu while open.
        e.preventDefault();
        ctx.focusItem(e.shiftKey ? -1 : 1);
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    const close = () => ctx.setOpen(false);
    document.addEventListener('mousedown', handleOutside);
    function handleOutside(e: MouseEvent) {
      if (!contentRef.current?.contains(e.target as Node) && !ctx.triggerRef.current?.contains(e.target as Node)) {
        close();
      }
    }
    return () => {
      clearTimeout(t);
      document.removeEventListener('keydown', handleKeyDown);
      document.removeEventListener('mousedown', handleOutside);
    };
  }, [ctx]);

  if (!ctx.open) return null;
  return (
    <div
      ref={contentRef}
      role="menu"
      aria-orientation="vertical"
      className={cn(
        'absolute z-50 mt-2 min-w-[12rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-lg',
        align === 'end' ? 'right-0' : 'left-0',
        className,
      )}
      onClick={(e) => e.stopPropagation()}
    >
      {children}
    </div>
  );
}

export function DropdownMenuItem({
  children,
  className,
  onClick,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const ctx = React.useContext(DropdownContext)!;
  const ref = React.useRef<HTMLButtonElement | null>(null);

  React.useEffect(() => {
    ctx.registerItem(ref.current);
    return () => ctx.registerItem(null);
  });

  return (
    <button
      ref={ref}
      type="button"
      role="menuitem"
      tabIndex={-1}
      onClick={(e) => {
        onClick?.(e);
        ctx.setOpen(false);
      }}
      className={cn(
        'relative flex w-full cursor-pointer select-none items-center gap-2 rounded-sm px-3 py-2 text-sm outline-none transition-colors',
        'hover:bg-accent hover:text-accent-foreground',
        'focus-inset',
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}

export function DropdownMenuLabel({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('px-3 py-2 text-sm font-medium', className)} role="presentation">
      {children}
    </div>
  );
}

export function DropdownMenuSeparator() {
  return <div className="my-1 h-px bg-muted" role="separator" />;
}

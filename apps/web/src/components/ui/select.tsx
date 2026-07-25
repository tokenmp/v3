'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, Check, Search } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface SelectOption {
  value: string;
  label: string;
  group?: string;
  description?: string;
}

/**
 * 自定义下拉选择（替代原生 select）。
 * 点击展开列表，键盘可达，点外部收起。
 * 支持 searchable：选项 >8 时开启搜索框。
 */
export function Select({
  value,
  options,
  onChange,
  placeholder = '请选择',
  disabled,
  searchable = false,
  className,
}: {
  value: string;
  options: SelectOption[];
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
  searchable?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const selected = options.find((o) => o.value === value);

  const filtered = useMemo(() => {
    if (!query.trim()) return options;
    const q = query.trim().toLowerCase();
    return options.filter(
      (o) =>
        o.label.toLowerCase().includes(q) ||
        o.value.toLowerCase().includes(q) ||
        (o.description?.toLowerCase().includes(q) ?? false),
    );
  }, [options, query]);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [open]);

  useEffect(() => {
    if (open && searchable) {
      setQuery('');
      requestAnimationFrame(() => searchRef.current?.focus());
    }
  }, [open, searchable]);

  return (
    <div ref={ref} className={cn('relative', className)}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className="flex h-[var(--control-height-sm)] w-full items-center justify-between rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
      >
        <span className={cn('truncate', !selected && 'text-muted-foreground')}>
          {selected ? selected.label : placeholder}
        </span>
        <ChevronDown
          className={cn(
            'size-3.5 shrink-0 text-muted-foreground transition-transform',
            open && 'rotate-180',
          )}
        />
      </button>
      {open ? (
        <div className="absolute z-50 mt-1 w-full rounded-sm border border-border bg-popover shadow-lg">
          {searchable ? (
            <div className="border-b border-border p-1.5">
              <div className="flex items-center gap-1.5 rounded-sm bg-muted/50 px-2">
                <Search className="size-3.5 shrink-0 text-muted-foreground" />
                <input
                  ref={searchRef}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索…"
                  className="h-7 w-full bg-transparent text-xs outline-none placeholder:text-muted-foreground"
                />
              </div>
            </div>
          ) : null}
          <ul className="max-h-60 overflow-auto py-1">
            {filtered.length === 0 ? (
              <li className="px-3 py-2 text-center text-xs text-muted-foreground">无匹配</li>
            ) : (
              filtered.map((o, i) => (
                <li key={o.value}>
                  {i > 0 && o.group && o.group !== filtered[i - 1]?.group ? (
                    <div className="my-1 border-t border-border" />
                  ) : null}
                  {i === 0 && o.group ? (
                    <p className="px-3 py-1 text-[10px] font-semibold uppercase text-muted-foreground">
                      {o.group}
                    </p>
                  ) : null}
                  <button
                    type="button"
                    onClick={() => {
                      onChange(o.value);
                      setOpen(false);
                    }}
                    className={cn(
                      'flex w-full items-start justify-between gap-2 px-3 py-1.5 text-left text-sm hover:bg-accent',
                      o.value === value && 'font-medium',
                    )}
                  >
                    <span className="min-w-0">
                      <span className="block truncate">{o.label}</span>
                      {o.description ? (
                        <span className="block truncate font-mono text-[10px] text-muted-foreground">
                          {o.description}
                        </span>
                      ) : null}
                    </span>
                    {o.value === value ? (
                      <Check className="size-3.5 shrink-0 text-primary" />
                    ) : null}
                  </button>
                </li>
              ))
            )}
          </ul>
        </div>
      ) : null}
    </div>
  );
}

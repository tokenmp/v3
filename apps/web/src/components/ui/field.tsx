'use client';

import { type ReactNode } from 'react';
import { Minus, Plus } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Select, type SelectOption } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

/**
 * 通用表单原子组件集合（admin 创建/编辑弹窗复用）。
 * - 统一 label + 必填红 * / 可选灰标
 * - hint / error 槽
 * - 复用 design token：--control-height-sm 等
 */

export const inputCls =
  'h-[var(--control-height-sm)] w-full rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50';

export const textareaCls =
  'w-full rounded-sm border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50';

export function FormSection({
  title,
  description,
  children,
  cols = 2,
  className,
}: {
  title?: string;
  description?: string;
  children: ReactNode;
  cols?: 1 | 2 | 3;
  className?: string;
}) {
  const grid =
    cols === 1
      ? 'grid gap-3'
      : cols === 3
        ? 'grid gap-3 sm:grid-cols-3'
        : 'grid gap-3 sm:grid-cols-2';
  return (
    <section className={cn('space-y-3', className)}>
      {title || description ? (
        <header className="space-y-0.5">
          {title ? (
            <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              {title}
            </p>
          ) : null}
          {description ? (
            <p className="text-[11px] leading-relaxed text-muted-foreground/80">{description}</p>
          ) : null}
        </header>
      ) : null}
      <div className={grid}>{children}</div>
    </section>
  );
}

export function Field({
  label,
  required,
  hint,
  error,
  colSpan = 1,
  children,
}: {
  label?: string;
  required?: boolean;
  hint?: string;
  error?: string;
  colSpan?: 1 | 2 | 3;
  children: ReactNode;
}) {
  const span = colSpan === 2 ? 'sm:col-span-2' : colSpan === 3 ? 'sm:col-span-3' : '';
  return (
    <div className={span}>
      {label ? (
        <label className="mb-1.5 flex items-baseline gap-1 text-xs font-medium text-foreground">
          {label}
          {required ? (
            <span aria-hidden className="text-destructive">*</span>
          ) : (
            <span className="text-[10px] font-normal text-muted-foreground/70">可选</span>
          )}
        </label>
      ) : null}
      {children}
      {error ? (
        <p className="mt-1 flex items-center gap-1 text-[11px] text-destructive">
          <span className="size-1 rounded-full bg-destructive" />
          {error}
        </p>
      ) : hint ? (
        <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground/80">{hint}</p>
      ) : null}
    </div>
  );
}

export function TextField({
  value,
  onChange,
  placeholder,
  type = 'text',
  disabled,
  className,
  ...rest
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: 'text' | 'number' | 'password' | 'url';
  disabled?: boolean;
  className?: string;
} & Omit<React.InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange' | 'type' | 'className'>) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      disabled={disabled}
      className={cn(inputCls, className)}
      {...rest}
    />
  );
}

export function NumberField({
  value,
  onChange,
  placeholder,
  min,
  max,
  step = 1,
  unit,
  disabled,
}: {
  value: string | number;
  onChange: (v: string) => void;
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
  unit?: string;
  disabled?: boolean;
}) {
  const num = typeof value === 'number' ? value : Number(value);
  const clamp = (n: number) => {
    if (min !== undefined && n < min) n = min;
    if (max !== undefined && n > max) n = max;
    return n;
  };
  return (
    <div className="relative flex items-center">
      <button
        type="button"
        tabIndex={-1}
        disabled={disabled || (min !== undefined && num <= min)}
        onClick={() => onChange(String(clamp(Number.isNaN(num) ? 0 : num - step)))}
        className="absolute left-1 inline-flex size-6 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-30"
        aria-label="减少"
      >
        <Minus className="size-3" />
      </button>
      <input
        type="number"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        className={cn(inputCls, 'px-8 text-center tabular-nums')}
      />
      <button
        type="button"
        tabIndex={-1}
        disabled={disabled || (max !== undefined && num >= max)}
        onClick={() => onChange(String(clamp(Number.isNaN(num) ? 0 : num + step)))}
        className="absolute right-1 inline-flex size-6 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-30"
        aria-label="增加"
      >
        <Plus className="size-3" />
      </button>
      {unit ? (
        <span className="pointer-events-none absolute right-8 text-[10px] text-muted-foreground">
          {unit}
        </span>
      ) : null}
    </div>
  );
}

export function TextAreaField({
  value,
  onChange,
  placeholder,
  rows = 3,
  disabled,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <textarea
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      rows={rows}
      disabled={disabled}
      className={cn(textareaCls, className)}
    />
  );
}

export function SelectField({
  value,
  onChange,
  options,
  placeholder = '请选择',
  disabled,
  searchable = false,
}: {
  value: string;
  onChange: (v: string) => void;
  options: (string | SelectOption)[];
  placeholder?: string;
  disabled?: boolean;
  searchable?: boolean;
}) {
  const norm: SelectOption[] = options.map((o) =>
    typeof o === 'string' ? { value: o, label: o } : o,
  );
  return (
    <Select
      value={value}
      onChange={onChange}
      options={norm}
      placeholder={placeholder}
      disabled={disabled}
      searchable={searchable || norm.length > 8}
    />
  );
}

export function SwitchField({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: string;
  disabled?: boolean;
}) {
  return <Switch checked={checked} onChange={onChange} label={label} disabled={disabled} />;
}

export function TabField({
  value,
  onChange,
  options,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  options: SelectOption[];
  disabled?: boolean;
}) {
  return (
    <div className="flex w-full flex-wrap gap-1 rounded-sm border border-border bg-muted/40 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          disabled={disabled}
          onClick={() => onChange(o.value)}
          className={cn(
            'inline-flex h-[calc(var(--control-height-sm)-4px)] flex-1 items-center justify-center gap-1 rounded-sm px-2 text-xs font-medium transition-colors disabled:opacity-50',
            o.value === value
              ? 'bg-primary text-primary-foreground shadow-sm'
              : 'text-muted-foreground hover:bg-accent hover:text-foreground',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function FormActions({
  onCancel,
  onSubmit,
  submitLabel = '创建',
  submitting,
  disabled,
  destructive,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  submitLabel?: string;
  submitting?: boolean;
  disabled?: boolean;
  destructive?: boolean;
}) {
  return (
    <div className="flex justify-end gap-2">
      <button
        type="button"
        onClick={onCancel}
        className="inline-flex h-[var(--control-height-sm)] items-center rounded-sm border border-border bg-card px-3 text-xs font-medium hover:bg-accent"
      >
        取消
      </button>
      <button
        type="button"
        onClick={onSubmit}
        disabled={disabled || submitting}
        className={cn(
          'inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm px-3 text-xs font-medium hover:opacity-90 disabled:opacity-50',
          destructive
            ? 'bg-destructive text-destructive-foreground'
            : 'bg-primary text-primary-foreground',
        )}
      >
        {submitting ? '处理中…' : submitLabel}
      </button>
    </div>
  );
}

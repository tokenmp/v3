'use client';

import { useEffect, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * 通用弹窗（基于原生 <dialog>）。
 * - title：标题栏
 * - children：弹窗内容
 * - footer：底部操作区（按钮等）
 * - open/onClose：受控
 */
export function Modal({
  open,
  title,
  description,
  children,
  footer,
  onClose,
  maxWidth = 'md',
}: {
  open: boolean;
  title: string;
  description?: string;
  children: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  maxWidth?: 'sm' | 'md' | 'lg';
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const pointerDownOnBackdropRef = useRef(false);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const onCancel = (e: Event) => {
      e.preventDefault();
      onClose();
    };
    d.addEventListener('cancel', onCancel);
    return () => d.removeEventListener('cancel', onCancel);
  }, [onClose]);

  const maxW = maxWidth === 'sm' ? 'max-w-sm' : maxWidth === 'lg' ? 'max-w-2xl' : 'max-w-md';

  return (
    <dialog
      ref={ref}
      className={cn(
        '!m-auto max-h-[85vh] w-[92vw] overflow-y-auto rounded-xl border border-border bg-card p-0 shadow-lg backdrop:bg-black/40',
        maxW,
      )}
      onPointerDown={(event) => {
        pointerDownOnBackdropRef.current = event.target === ref.current;
      }}
      onPointerUp={(event) => {
        const releasedOnBackdrop = event.target === ref.current;
        if (pointerDownOnBackdropRef.current && releasedOnBackdrop) onClose();
        pointerDownOnBackdropRef.current = false;
      }}
      onPointerCancel={() => {
        pointerDownOnBackdropRef.current = false;
      }}
    >
      <div className="flex items-center justify-between border-b border-border px-5 py-3">
        <div>
          <p className="text-sm font-medium">{title}</p>
          {description ? (
            <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
          ) : null}
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-sm p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          aria-label="关闭"
        >
          <X className="size-4" />
        </button>
      </div>
      <div className="px-5 py-4">{children}</div>
      {footer ? (
        <div className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>
      ) : null}
    </dialog>
  );
}

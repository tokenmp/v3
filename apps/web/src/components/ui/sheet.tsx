'use client';

import { useEffect, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * 右侧抽屉（基于原生 <dialog>）。
 * 用于详情/配置等较重的二级面板，比 Modal 更适合长内容。
 * - title / description：标题栏
 * - children：可滚动正文
 * - footer：底部操作区（按钮等）
 * - open/onClose：受控
 * - width：sm/md/lg/xl/2xl
 */
export function Sheet({
  open,
  title,
  description,
  children,
  footer,
  onClose,
  width = 'lg',
}: {
  open: boolean;
  title: string;
  description?: string;
  children: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  width?: 'sm' | 'md' | 'lg' | 'xl' | '2xl';
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

  const w =
    width === 'sm' ? 'sm:max-w-sm' :
    width === 'md' ? 'sm:max-w-md' :
    width === 'xl' ? 'sm:max-w-3xl' :
    width === '2xl' ? 'sm:max-w-4xl' :
    'sm:max-w-lg';

  return (
    <dialog
      ref={ref}
      className={cn(
        '!m-0 !ml-auto h-dvh max-h-dvh w-[92vw] border-l border-border bg-card p-0 shadow-xl backdrop:bg-black/40',
        w,
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
      <div className="flex h-dvh max-h-dvh flex-col">
        <div className="flex items-center justify-between border-b border-border px-5 py-3">
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold">{title}</p>
            {description ? (
              <p className="mt-0.5 truncate text-xs text-muted-foreground">{description}</p>
            ) : null}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
            aria-label="关闭"
          >
            <X className="size-4" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer ? (
          <div className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>
        ) : null}
      </div>
    </dialog>
  );
}

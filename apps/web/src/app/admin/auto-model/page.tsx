'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Sparkles } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import { PublishStatusHint } from '@/components/publish-status-hint';
import { PageHeader } from '@/components/page-header';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { AdminModelConfig } from '@/types/admin';

/**
 * Auto 模型管理页.
 *
 * The reserved "auto" model selector lets clients request model="auto" and
 * have the executor pick the first eligible candidate from a configured,
 * ordered pool (Global.AutoModelIDs). Without an explicit config the
 * executor falls back to all active models sorted by ID — which is rarely
 * what an operator wants. This page lets admins pick which active models
 * join the auto pool and in what order, then publish from System Settings.
 */
export default function AdminAutoModelPage() {
  const qc = useQueryClient();
  const { data: models = [], isLoading: modelsLoading } = useQuery({
    queryKey: ['admin', 'model-configs'] as const,
    queryFn: adminConfigApi.listModels,
  });
  const { data: global, isLoading: globalLoading } = useQuery({
    queryKey: ['admin', 'global-policy'] as const,
    queryFn: adminConfigApi.getGlobalPolicy,
  });

  const allModels = useMemo(() => models, [models]);

  // The ordered draft of selected model IDs.
  const [selected, setSelected] = useState<string[] | null>(null);

  // Lazily initialize the draft once both queries have loaded.
  const draft: string[] = useMemo(() => {
    if (selected !== null) return selected;
    const configured = global?.auto_model_ids ?? [];
    // Only keep IDs that still exist as active models (config may reference
    // deleted/inactive models; those are silently dropped on save anyway).
    const activeIDs = new Set(allModels.map((m) => m.id));
    const kept = configured.filter((id) => activeIDs.has(id));
    // Append any active models not in the config (so the operator sees them
    // and can opt-in) — but only when the config was non-empty; an empty
    // config means "use default (all active)", which we reflect by selecting
    // all active models so the UI is honest about the fallback.
    if (configured.length === 0) {
      return allModels.map((m) => m.id).sort();
    }
    return kept;
  }, [selected, global, allModels]);

  const setDraft = (next: string[]) => setSelected(next);

  const selectedSet = useMemo(() => new Set(draft), [draft]);

  const save = useMutation({
    mutationFn: (ids: string[]) => adminConfigApi.setAutoModelIds(ids),
    onSuccess: () => {
      toast.success('已保存 auto_model_ids（需点「编译并发布」生效）');
      setSelected(null);
      void qc.invalidateQueries({ queryKey: ['admin', 'global-policy'] });
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const move = (id: string, dir: -1 | 1) => {
    const idx = draft.indexOf(id);
    const target = idx + dir;
    if (idx < 0 || target < 0 || target >= draft.length) return;
    const next = draft.slice();
    const a = next[idx];
    const b = next[target];
    next[idx] = b as string;
    next[target] = a as string;
    setDraft(next);
  };

  const toggle = (id: string) => {
    if (selectedSet.has(id)) {
      setDraft(draft.filter((x) => x !== id));
    } else {
      setDraft([...draft, id]);
    }
  };

  const loading = modelsLoading || globalLoading;

  return (
    <div className="space-y-4">
      <PageHeader title="Auto 模型" actions={<PublishStatusHint />} />

      <p className="text-sm text-muted-foreground">
        客户端请求 <code className="rounded bg-muted px-1">model=auto</code> 时，executor 从下方已选模型池中按顺序选第一个可用的。拖动顺序或勾选模型，保存后到系统设置统一发布生效。
      </p>

      <div className="flex items-center gap-2">
        <Button
          size="sm"
          onClick={() => save.mutate(draft)}
          disabled={save.isPending || loading}
        >
          <Sparkles className="h-4 w-4 mr-1" />
          {save.isPending ? '保存中…' : '保存配置'}
        </Button>
        <span className="text-muted-foreground">
          已选 {draft.length} / {allModels.length} 个模型
        </span>
      </div>

      {loading ? (
        <div className="rounded-lg border p-8 text-center text-sm text-muted-foreground">加载中…</div>
      ) : (
        <>
        <div className="hidden md:block">
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead className="w-10">序</TableHead>
                <TableHead>模型 ID</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>能力</TableHead>
                <TableHead className="w-10 text-center">入选</TableHead>
                <TableHead className="w-24 text-right">排序</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {allModels.map((m: AdminModelConfig) => {
                const inPool = selectedSet.has(m.id);
                const orderIdx = inPool ? draft.indexOf(m.id) : -1;
                return (
                  <TableRow key={m.id} className={cn(!inPool && 'opacity-60')}>
                    <TableCell className="font-mono text-muted-foreground">
                      {orderIdx >= 0 ? orderIdx + 1 : '—'}
                    </TableCell>
                    <TableCell className="font-mono">{m.id}</TableCell>
                    <TableCell>{m.displayName}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {(m.capabilities ?? []).join(', ') || '—'}
                    </TableCell>
                    <TableCell className="text-center">
                      <input
                        type="checkbox"
                        checked={inPool}
                        onChange={() => toggle(m.id)}
                        className="h-4 w-4 rounded border-input"
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      {inPool && (
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            onClick={() => move(m.id, -1)}
                            disabled={orderIdx === 0}
                          >
                            <ArrowUp className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0"
                            onClick={() => move(m.id, 1)}
                            disabled={orderIdx === draft.length - 1}
                          >
                            <ArrowDown className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
        </div>

        <div className="md:hidden space-y-3">
          {allModels.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无模型</p>
          ) : allModels.map((m: AdminModelConfig) => {
            const inPool = selectedSet.has(m.id);
            const orderIdx = inPool ? draft.indexOf(m.id) : -1;
            return (
              <div key={m.id} className={cn('rounded-lg border bg-card p-3 space-y-2', !inPool && 'opacity-60')}>
                <div className="flex items-center justify-between gap-2">
                  <div className="min-w-0">
                    <p className="text-sm font-medium truncate">{m.displayName}</p>
                    <p className="font-mono text-[10px] text-muted-foreground truncate">{m.id}</p>
                  </div>
                  <input
                    type="checkbox"
                    checked={inPool}
                    onChange={() => toggle(m.id)}
                    className="h-4 w-4 rounded border-input"
                  />
                </div>
                <div className="text-muted-foreground">
                  {(m.capabilities ?? []).join(', ') || '—'}
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs text-muted-foreground tabular-nums">
                    序 {orderIdx >= 0 ? orderIdx + 1 : '—'}
                  </span>
                  {inPool && (
                    <div className="flex gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0"
                        onClick={() => move(m.id, -1)}
                        disabled={orderIdx === 0}
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0"
                        onClick={() => move(m.id, 1)}
                        disabled={orderIdx === draft.length - 1}
                      >
                        <ArrowDown className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
        </>
      )}
    </div>
  );
}

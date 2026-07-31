'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Save, Sparkles } from 'lucide-react';
import { toast } from 'sonner';
import { request } from '@/lib/api/core';
import { userApi } from '@/lib/api/user';
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

interface CatalogModel {
  id: string;
  capabilities?: string[];
}

interface CatalogResponse {
  object: 'list';
  data: CatalogModel[];
}

async function listCatalogModels(): Promise<CatalogModel[]> {
  const res = await request<CatalogResponse>('/v1/models');
  return res.data ?? [];
}

/**
 * Auto 模型配置页（用户自助）.
 *
 * The reserved "auto" model selector lets you call model="auto" and have the
 * platform pick the first available model from YOUR configured, ordered pool.
 * When you don't configure one, the platform default applies. This page lets
 * you pick which models join your auto pool and in what order.
 */
export default function PanelAutoModelPage() {
  const qc = useQueryClient();
  const { data: catalog = [], isLoading: catalogLoading } = useQuery({
    queryKey: ['catalog', 'models'] as const,
    queryFn: listCatalogModels,
  });
  const { data: configured = [], isLoading: configLoading } = useQuery({
    queryKey: ['user', 'auto-models'] as const,
    queryFn: userApi.getAutoModels,
  });

  // The ordered draft. Lazily initialized from server config; tracks local
  // edits until saved. null = "not yet initialized from server".
  const [draft, setDraft] = useState<string[] | null>(null);

  const pool: string[] = useMemo(() => {
    if (draft !== null) return draft;
    if (configured.length === 0) return [];
    // Only keep IDs that still exist in the catalog (a model may have been
    // removed since the user last configured).
    const ids = new Set(catalog.map((m) => m.id));
    return configured.filter((id) => ids.has(id));
  }, [draft, configured, catalog]);

  const poolSet = useMemo(() => new Set(pool), [pool]);

  const save = useMutation({
    mutationFn: (ids: string[]) => userApi.updateAutoModels(ids),
    onSuccess: (saved) => {
      toast.success(saved.length ? '已保存 Auto 模型配置' : '已恢复平台默认');
      setDraft(null);
      void qc.invalidateQueries({ queryKey: ['user', 'auto-models'] });
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const toggle = (id: string) => {
    if (poolSet.has(id)) {
      setDraft(pool.filter((x) => x !== id));
    } else {
      setDraft([...pool, id]);
    }
  };

  const move = (id: string, dir: -1 | 1) => {
    const idx = pool.indexOf(id);
    const target = idx + dir;
    if (idx < 0 || target < 0 || target >= pool.length) return;
    const next = pool.slice();
    const a = next[idx];
    const b = next[target];
    next[idx] = b as string;
    next[target] = a as string;
    setDraft(next);
  };

  const loading = catalogLoading || configLoading;

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold">Auto 模型</h1>
        <p className="text-sm text-muted-foreground">
          调用 <code className="rounded bg-muted px-1">model=auto</code> 时，平台从你配置的模型池中按顺序选第一个可用的。未配置时使用平台默认。
        </p>
      </div>

      <div className="flex items-center gap-2">
        <Button size="sm" onClick={() => save.mutate(pool)} disabled={save.isPending || loading}>
          <Save className="h-4 w-4 mr-1" />
          {save.isPending ? '保存中…' : '保存配置'}
        </Button>
        {pool.length > 0 && (
          <Button size="sm" variant="ghost" onClick={() => setDraft([])} disabled={save.isPending}>
            恢复平台默认
          </Button>
        )}
        <span className="text-muted-foreground">已选 {pool.length} / {catalog.length} 个模型</span>
      </div>

      {pool.length === 0 && !loading && (
        <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
          <Sparkles className="mx-auto mb-2 h-5 w-5" />
          未配置 Auto 模型，当前使用平台默认。勾选下方模型加入你的 Auto 池。
        </div>
      )}

      {loading ? (
        <div className="rounded-lg border p-8 text-center text-sm text-muted-foreground">加载中…</div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead className="w-10">序</TableHead>
                <TableHead>模型 ID</TableHead>
                <TableHead>能力</TableHead>
                <TableHead className="w-10 text-center">入选</TableHead>
                <TableHead className="w-24 text-right">排序</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {catalog.map((m) => {
                const inPool = poolSet.has(m.id);
                const orderIdx = inPool ? pool.indexOf(m.id) : -1;
                return (
                  <TableRow key={m.id} className={cn(!inPool && 'opacity-60')}>
                    <TableCell className="font-mono text-muted-foreground">
                      {orderIdx >= 0 ? orderIdx + 1 : '—'}
                    </TableCell>
                    <TableCell className="font-mono">{m.id}</TableCell>
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
                            disabled={orderIdx === pool.length - 1}
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
      )}
    </div>
  );
}

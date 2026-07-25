'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import { userApi } from '@/lib/api/user';
import { Button } from '@/components/ui/button';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { FilterChip } from '@/components/filter-chip';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminApiKey } from '@/types/admin';

function formatTime(iso: string | null) {
  return iso ? new Date(iso).toLocaleString('zh-CN') : '—';
}

export default function AdminApiKeysPage() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<string | undefined>(undefined);
  const [revokeTarget, setRevokeTarget] = useState<AdminApiKey | null>(null);

  const { data } = useQuery({
    queryKey: ['admin', 'keys', page],
    queryFn: () => adminApi.listKeys(page, 20),
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => userApi.revokeKey(id),
    onSuccess: () => {
      setRevokeTarget(null);
      queryClient.invalidateQueries({ queryKey: ['admin', 'keys'] });
      toast.success('密钥已撤销');
    },
    onError: () => toast.error('撤销密钥失败'),
  });

  const keys = data?.keys ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 20));

  const filtered = keys.filter((k) => {
    if (statusF && k.status !== statusF) return false;
    if (search) {
      const q = search.toLowerCase();
      const matches =
        k.name?.toLowerCase().includes(q) ||
        k.userEmail?.toLowerCase().includes(q) ||
        k.keyPrefix?.toLowerCase().includes(q);
      if (!matches) return false;
    }
    return true;
  });

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">API 密钥</h1>

      {/* 工具栏：搜索框左 + 筛选 chip 右 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-2">
          <input
            type="text"
            placeholder="搜索名称 / 用户 / 密钥前缀"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-[var(--control-height-sm)] min-w-56 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
        <div className="flex flex-wrap gap-1.5 text-xs">
          <FilterChip label="全部" active={!statusF} onClick={() => setStatusF(undefined)} />
          <FilterChip label="活跃" active={statusF === 'active'} onClick={() => setStatusF('active')} />
          <FilterChip label="已禁用" active={statusF === 'disabled'} onClick={() => setStatusF('disabled')} />
        </div>
      </div>

      {/* 表格 */}
      <div className="rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead className="text-xs">用户</TableHead>
                <TableHead className="text-xs">名称</TableHead>
                <TableHead className="text-xs">密钥</TableHead>
                <TableHead className="text-xs">状态</TableHead>
                <TableHead className="text-xs">创建时间</TableHead>
                <TableHead className="text-xs">最近使用</TableHead>
                <TableHead className="text-xs">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((k: AdminApiKey) => (
                <TableRow key={k.id}>
                  <TableCell className="text-sm">{k.userEmail}</TableCell>
                  <TableCell className="text-sm font-medium">{k.name}</TableCell>
                  <TableCell className="font-mono text-xs">{k.keyPrefix}…{k.keySuffix}</TableCell>
                  <TableCell>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${k.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-muted text-muted-foreground'}`}>
                      {k.status === 'active' ? '活跃' : '已禁用'}
                    </span>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(k.createdAt)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(k.lastUsedAt)}</TableCell>
                  <TableCell>
                    {k.status === 'active' && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => setRevokeTarget(k)}
                      >
                        撤销
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {filtered.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                    暂无 API 密钥
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* 分页 */}
      <div className="flex items-center justify-between gap-4 px-1 py-1 text-sm">
        <p className="text-xs text-muted-foreground">共 {total} 条</p>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="rounded-sm border border-border p-1.5 text-muted-foreground hover:bg-accent disabled:opacity-40 disabled:hover:bg-transparent"
            aria-label="上一页"
          >
            <ChevronLeft className="size-3.5" />
          </button>
          <span className="px-2 text-xs tabular-nums">{page} / {totalPages}</span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            className="rounded-sm border border-border p-1.5 text-muted-foreground hover:bg-accent disabled:opacity-40 disabled:hover:bg-transparent"
            aria-label="下一页"
          >
            <ChevronRight className="size-3.5" />
          </button>
        </div>
      </div>

      <ConfirmDialog
        open={!!revokeTarget}
        onOpenChange={(open) => !open && setRevokeTarget(null)}
        title="撤销密钥"
        description={`确定要撤销用户 ${revokeTarget?.userEmail} 的密钥「${revokeTarget?.name}」吗？此操作不可恢复。`}
        confirmText="确认撤销"
        destructive
        loading={revokeMutation.isPending}
        onConfirm={() => revokeTarget && revokeMutation.mutate(revokeTarget.id)}
      />
    </div>
  );
}

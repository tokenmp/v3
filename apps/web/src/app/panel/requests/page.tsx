'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { userApi } from '@/lib/api/user';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { FilterChip } from '@/components/filter-chip';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import type { RequestLog } from '@/types';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function RequestsPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<string | undefined>(undefined);

  const { data } = useQuery({
    queryKey: ['requests', page],
    queryFn: () => userApi.getRequests(page, 10),
    // Keep in-flight rows live so users see processing → terminal transitions.
    refetchInterval: 2_000,
  });

  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 10));

  const filtered = items.filter((r) => {
    if (statusF && r.status !== statusF) return false;
    if (search) {
      const q = search.toLowerCase();
      if (!r.model?.toLowerCase().includes(q) && !r.requestId?.toLowerCase().includes(q)) return false;
    }
    return true;
  });

  return (
    <div className="space-y-4">
      {/* 工具栏：搜索框左 + 筛选 chip 右 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-2">
          <input
            type="text"
            placeholder="搜索模型 / Request ID"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-[var(--control-height-sm)] min-w-48 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
        <div className="flex flex-wrap gap-1.5 text-xs">
          <FilterChip label="全部" active={!statusF} onClick={() => setStatusF(undefined)} />
          <FilterChip label="成功" active={statusF === 'success'} onClick={() => setStatusF('success')} />
          <FilterChip label="失败" active={statusF === 'error'} onClick={() => setStatusF('error')} />
          <FilterChip label="处理中" active={statusF === 'processing'} onClick={() => setStatusF('processing')} />
        </div>
      </div>

      {/* 表格 */}
      <div className="hidden md:block rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead className="text-xs">时间</TableHead>
                <TableHead className="text-xs">模型</TableHead>
                <TableHead className="text-xs">状态</TableHead>
                <TableHead className="text-xs">耗时</TableHead>
                <TableHead className="text-xs">输入 Token</TableHead>
                <TableHead className="text-xs">输出 Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((r: RequestLog) => (
                <TableRow key={r.requestId}>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(r.createdAt)}</TableCell>
                  <TableCell className="text-sm">{r.model}</TableCell>
                  <TableCell>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${r.status === 'success' ? 'bg-green-100 text-green-700' : r.status === 'processing' ? 'bg-blue-100 text-blue-700 animate-pulse' : 'bg-red-100 text-red-700'}`}>
                      {r.status === 'success' ? '成功' : r.status === 'processing' ? '处理中' : '失败'}
                    </span>
                  </TableCell>
                  <TableCell className="text-sm">{r.durationMs ?? '—'}ms</TableCell>
                  <TableCell className="text-sm">{(r.inputTokens ?? 0).toLocaleString()}</TableCell>
                  <TableCell className="text-sm">{(r.outputTokens ?? 0).toLocaleString()}</TableCell>
                </TableRow>
              ))}
              {filtered.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                    暂无请求记录
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {filtered.map((r: RequestLog) => (
          <div key={r.requestId} className="rounded-lg border bg-card p-3 space-y-2">
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium truncate">{r.model || '—'}</span>
              <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${r.status === 'success' ? 'bg-green-100 text-green-700' : r.status === 'processing' ? 'bg-blue-100 text-blue-700 animate-pulse' : 'bg-red-100 text-red-700'}`}>
                {r.status === 'success' ? '成功' : r.status === 'processing' ? '处理中' : '失败'}
              </span>
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <span>{r.durationMs ?? '—'}ms</span>
              <span className="tabular-nums">{(r.inputTokens ?? 0).toLocaleString()} / {(r.outputTokens ?? 0).toLocaleString()}</span>
            </div>
            <p className="text-[10px] text-muted-foreground">{formatTime(r.createdAt)}</p>
          </div>
        ))}
        {filtered.length === 0 && (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无请求记录</p>
        )}
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
    </div>
  );
}

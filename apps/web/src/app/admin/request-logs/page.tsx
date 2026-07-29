'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { adminApi } from '@/lib/api/admin';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { FilterChip } from '@/components/filter-chip';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import type { AdminRequestLog } from '@/types/admin';

function statusBadge(status: string) {
  if (status === 'success') return { label: '成功', cls: 'bg-green-100 text-green-700' };
  if (status === 'processing') return { label: '处理中', cls: 'bg-blue-100 text-blue-700 animate-pulse' };
  return { label: '失败', cls: 'bg-red-100 text-red-700' };
}

function formatTime(iso: string) {
  if (!iso) return '-';
  return new Date(iso).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
    timeZoneName: 'short',
  });
}

function formatUser(log: AdminRequestLog) {
  if (log.userEmail) return log.userEmail;
  if (log.userId) return log.userId.length > 12 ? log.userId.slice(0, 12) + '…' : log.userId;
  return '—';
}

export default function AdminRequestLogsPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<string | undefined>(undefined);

  const { data } = useQuery({
    queryKey: ['admin', 'request-logs', page],
    queryFn: () => adminApi.listRequestLogs(page, 20),
    // Keep in-flight rows live so users see processing → terminal transitions.
    refetchInterval: 2_000,
  });

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 20));

  const filtered = logs.filter((log) => {
    if (statusF && log.status !== statusF) return false;
    if (search) {
      const q = search.toLowerCase();
      const matches =
        log.requestId?.toLowerCase().includes(q) ||
        log.model?.toLowerCase().includes(q) ||
        log.userEmail?.toLowerCase().includes(q) ||
        log.userId?.toLowerCase().includes(q);
      if (!matches) return false;
    }
    return true;
  });

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">请求日志</h1>

      {/* 工具栏：搜索框左 + 筛选 chip 右 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-2">
          <input
            type="text"
            placeholder="搜索 Request ID / 用户 / 模型"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-[var(--control-height-sm)] min-w-56 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
                <TableHead className="text-xs">用户</TableHead>
                <TableHead className="text-xs">模型</TableHead>
                <TableHead className="text-xs">状态</TableHead>
                <TableHead className="text-xs">耗时</TableHead>
                <TableHead className="text-xs">输入 Token</TableHead>
                <TableHead className="text-xs">输出 Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((log) => (
                <TableRow key={log.requestId} className="cursor-pointer">
                  <TableCell className="text-xs text-muted-foreground">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {formatTime(log.createdAt)}
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {formatUser(log)}
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {log.model}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadge(log.status).cls}`}>
                        {statusBadge(log.status).label}
                      </span>
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {log.durationMs ?? '—'}ms
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {(log.inputTokens ?? 0).toLocaleString()}
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                      {(log.outputTokens ?? 0).toLocaleString()}
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
              {filtered.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
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
        {filtered.map((log) => (
          <Link
            key={log.requestId}
            href={`/admin/request-logs/${log.requestId}`}
            className="block rounded-lg border bg-card p-3 hover:bg-accent/50 transition-colors"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium truncate">{log.model || '—'}</span>
              <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadge(log.status).cls}`}>
                {statusBadge(log.status).label}
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground truncate">{formatUser(log)}</p>
            <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
              <span>{log.durationMs ?? '—'}ms</span>
              <span className="tabular-nums">{(log.inputTokens ?? 0).toLocaleString()} / {(log.outputTokens ?? 0).toLocaleString()}</span>
            </div>
            <p className="mt-1 text-[10px] text-muted-foreground">{formatTime(log.createdAt)}</p>
          </Link>
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

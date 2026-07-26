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
          <FilterChip label="失败" active={statusF === 'failed'} onClick={() => setStatusF('failed')} />
        </div>
      </div>

      {/* 表格 */}
      <div className="rounded-md border border-border bg-card">
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
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${log.status === 'success' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
                        {log.status === 'success' ? '成功' : '失败'}
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

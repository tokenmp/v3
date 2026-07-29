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
import {
  formatDuration,
  formatTokens,
  formatTokensPerSecond,
  calcTokensPerSecond,
  protocolLabel,
  streamLabel,
  truncateUA,
  thinkingLabel,
} from '@/lib/request-log-metrics';

function statusBadge(status: string) {
  if (status === 'success') return { label: '成功', cls: 'bg-green-100 text-green-700' };
  if (status === 'processing') return { label: '处理中', cls: 'bg-blue-100 text-blue-700 animate-pulse' };
  if (status === 'cancelled') return { label: '已取消', cls: 'bg-amber-100 text-amber-700' };
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

function shortRequestId(id: string | null | undefined): string {
  if (!id) return '—';
  return id.length > 10 ? `…${id.slice(-10)}` : id;
}

function speedFor(log: AdminRequestLog): number | null {
  return calcTokensPerSecond({
    outputTokens: log.outputTokens,
    durationMs: log.durationMs,
    ttftMs: log.ttftMs,
    stream: log.stream,
  });
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
          <FilterChip label="已取消" active={statusF === 'cancelled'} onClick={() => setStatusF('cancelled')} />
          <FilterChip label="处理中" active={statusF === 'processing'} onClick={() => setStatusF('processing')} />
        </div>
      </div>

      {/* Desktop table */}
      <div className="hidden md:block rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead className="text-xs whitespace-nowrap">请求ID</TableHead>
                <TableHead className="text-xs whitespace-nowrap">用户</TableHead>
                <TableHead className="text-xs whitespace-nowrap">模型</TableHead>
                <TableHead className="text-xs whitespace-nowrap">协议</TableHead>
                <TableHead className="text-xs whitespace-nowrap">Provider</TableHead>
                <TableHead className="text-xs whitespace-nowrap">状态</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">输入</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">输出</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">缓存</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">TTFT</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">速度</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">耗时</TableHead>
                <TableHead className="text-xs whitespace-nowrap">Thinking</TableHead>
                <TableHead className="text-xs whitespace-nowrap">UA</TableHead>
                <TableHead className="text-xs whitespace-nowrap">时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((log) => {
                const speed = speedFor(log);
                return (
                  <TableRow key={log.requestId} className="cursor-pointer">
                    <TableCell className="text-xs font-mono whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} title={log.requestId} className="block">
                        {shortRequestId(log.requestId)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm max-w-[180px]">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block truncate">
                        {formatUser(log)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        {log.model || '—'}
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        {protocolLabel(log.protocol)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        {log.provider ?? '—'}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadge(log.status).cls}`}>
                          {statusBadge(log.status).label}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatTokens(log.inputTokens)}</Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatTokens(log.outputTokens)}</Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatTokens(log.cacheTokens)}</Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatDuration(log.ttftMs)}</Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatTokensPerSecond(speed)}</Link>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">{formatDuration(log.durationMs)}</Link>
                    </TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        {thinkingLabel(log.thinkingEffectiveEffort ?? log.thinkingEffort, log.thinkingMode, log.thinkingRequestedEffort, log.thinkingEffortDegraded)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-sm max-w-[160px]">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        <span title={log.userAgent ?? undefined} className="block truncate">
                          {truncateUA(log.userAgent)}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                      <Link href={`/admin/request-logs/${log.requestId}`} className="block">
                        {formatTime(log.createdAt)}
                      </Link>
                    </TableCell>
                  </TableRow>
                );
              })}
              {filtered.length === 0 && (
                <TableRow>
                  <TableCell colSpan={15} className="py-8 text-center text-sm text-muted-foreground">
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
        {filtered.map((log) => {
          const speed = calcTokensPerSecond({
            outputTokens: log.outputTokens,
            durationMs: log.durationMs,
            ttftMs: log.ttftMs,
            stream: log.stream,
          });
          return (
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
              {/* Request type row */}
              <div className="mt-1.5 flex items-center gap-2 text-xs text-muted-foreground">
                <span>{protocolLabel(log.protocol)}</span>
                {log.stream != null && (
                  <span className={`rounded px-1 py-px text-[10px] font-medium ${log.stream ? 'bg-blue-100 text-blue-700' : 'bg-muted text-muted-foreground'}`}>
                    {streamLabel(log.stream)}
                  </span>
                )}
                {log.provider && <span>· {log.provider}</span>}
              </div>
              {/* Token row */}
              <div className="mt-1.5 flex items-center justify-between text-xs">
                <span className="tabular-nums">
                  {formatTokens(log.inputTokens)} / {formatTokens(log.outputTokens)}
                  {log.cacheTokens != null && log.cacheTokens > 0 && (
                    <span className="text-muted-foreground"> ({formatTokens(log.cacheTokens)}缓存)</span>
                  )}
                </span>
                <span>{formatDuration(log.durationMs)}</span>
              </div>
              {/* Performance row */}
              <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                {log.ttftMs != null && log.ttftMs > 0 && log.stream === true && (
                  <span>TTFT {formatDuration(log.ttftMs)}</span>
                )}
                {speed != null && <span>{formatTokensPerSecond(speed)}</span>}
                {(log.thinkingEffectiveEffort ?? log.thinkingEffort) && <span>思考: {thinkingLabel(log.thinkingEffectiveEffort ?? log.thinkingEffort, log.thinkingMode, log.thinkingRequestedEffort, log.thinkingEffortDegraded)}</span>}
              </div>
              {/* UA + time */}
              <div className="mt-1.5 flex items-center justify-between text-[10px] text-muted-foreground">
                <span>{formatTime(log.createdAt)}</span>
                {log.userAgent && (
                  <span className="truncate max-w-[50%]" title={log.userAgent}>{truncateUA(log.userAgent, 20)}</span>
                )}
              </div>
            </Link>
          );
        })}
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

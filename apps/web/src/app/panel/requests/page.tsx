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
import {
  formatDuration,
  formatTokens,
  formatTokensPerSecond,
  calcTokensPerSecond,
  protocolLabel,
  streamLabel,
  thinkingLabel,
} from '@/lib/request-log-metrics';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function statusBadge(status: string) {
  if (status === 'success') return { label: '成功', cls: 'bg-green-100 text-green-700' };
  if (status === 'processing') return { label: '处理中', cls: 'bg-blue-100 text-blue-700 animate-pulse' };
  if (status === 'cancelled') return { label: '已取消', cls: 'bg-amber-100 text-amber-700' };
  return { label: '失败', cls: 'bg-red-100 text-red-700' };
}

function shortRequestId(id: string | null | undefined): string {
  if (!id) return '—';
  return id.length > 10 ? `…${id.slice(-10)}` : id;
}

function speedFor(r: RequestLog): number | null {
  return calcTokensPerSecond({
    outputTokens: r.outputTokens,
    durationMs: r.durationMs,
    ttftMs: r.ttftMs,
    stream: r.stream,
    createdAt: r.createdAt,
    completedAt: r.completedAt,
  });
}

export default function RequestsPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<string | undefined>(undefined);

  const setStatusFilter = (status: string | undefined) => {
    setPage(1);
    setStatusF(status);
  };

  const searchTerm = search.trim();

  const { data } = useQuery({
    queryKey: ['requests', page, searchTerm, statusF ?? 'all'],
    queryFn: () => userApi.getRequests(page, 10, searchTerm, statusF ?? ''),
    // Keep in-flight rows live so users see processing → terminal transitions.
    refetchInterval: 2_000,
  });

  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 10));

  return (
    <div className="space-y-4">
      {/* 工具栏：搜索框左 + 筛选 chip 右 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-2">
          <input
            type="text"
            placeholder="搜索模型 / Request ID"
            value={search}
            onChange={(e) => {
              setPage(1);
              setSearch(e.target.value);
            }}
            className="h-[var(--control-height-sm)] min-w-48 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
        <div className="flex flex-wrap gap-1.5 text-xs">
          <FilterChip label="全部" active={!statusF} onClick={() => setStatusFilter(undefined)} />
          <FilterChip label="成功" active={statusF === 'success'} onClick={() => setStatusFilter('success')} />
          <FilterChip label="失败" active={statusF === 'error'} onClick={() => setStatusFilter('error')} />
          <FilterChip label="已取消" active={statusF === 'cancelled'} onClick={() => setStatusFilter('cancelled')} />
          <FilterChip label="处理中" active={statusF === 'processing'} onClick={() => setStatusFilter('processing')} />
        </div>
      </div>

      {/* Desktop table */}
      <div className="hidden md:block rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead className="text-xs whitespace-nowrap">请求ID</TableHead>
                <TableHead className="text-xs whitespace-nowrap">模型</TableHead>
                <TableHead className="text-xs whitespace-nowrap">协议</TableHead>
                <TableHead className="text-xs whitespace-nowrap">状态</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">输入</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">输出</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">缓存</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">TTFT</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">速度</TableHead>
                <TableHead className="text-xs whitespace-nowrap text-right">耗时</TableHead>
                <TableHead className="text-xs whitespace-nowrap">Thinking</TableHead>
                <TableHead className="text-xs whitespace-nowrap">时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r: RequestLog) => {
                const speed = speedFor(r);
                return (
                  <TableRow key={r.requestId}>
                    <TableCell className="text-xs font-mono whitespace-nowrap" title={r.requestId}>{shortRequestId(r.requestId)}</TableCell>
                    <TableCell className="text-sm whitespace-nowrap">{r.model || '—'}</TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <span className="flex items-center gap-1.5">
                        <span>{protocolLabel(r.protocol)}</span>
                        {r.stream != null && (
                          <span className={`rounded px-1 py-px text-[10px] font-medium ${r.stream ? 'bg-blue-100 text-blue-700' : 'bg-muted text-muted-foreground'}`}>
                            {streamLabel(r.stream)}
                          </span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadge(r.status).cls}`}>
                        {statusBadge(r.status).label}
                      </span>
                    </TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatTokens(r.inputTokens)}</TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatTokens(r.outputTokens)}</TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatTokens(r.cacheTokens)}</TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatDuration(r.ttftMs)}</TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatTokensPerSecond(speed)}</TableCell>
                    <TableCell className="text-sm text-right tabular-nums">{formatDuration(r.durationMs)}</TableCell>
                    <TableCell className="text-sm whitespace-nowrap">{thinkingLabel(r.thinkingEffectiveEffort ?? r.thinkingEffort, null, r.thinkingRequestedEffort, r.thinkingEffortDegraded)}</TableCell>
                    <TableCell className="text-xs text-muted-foreground whitespace-nowrap">{formatTime(r.createdAt)}</TableCell>
                  </TableRow>
                );
              })}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={12} className="py-8 text-center text-sm text-muted-foreground">
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
        {items.map((r: RequestLog) => {
          const speed = calcTokensPerSecond({
            outputTokens: r.outputTokens,
            durationMs: r.durationMs,
            ttftMs: r.ttftMs,
            stream: r.stream,
            createdAt: r.createdAt,
            completedAt: r.completedAt,
          });
          return (
            <div key={r.requestId} className="rounded-lg border bg-card p-3 space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium truncate">{r.model || '—'}</span>
                <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadge(r.status).cls}`}>
                  {statusBadge(r.status).label}
                </span>
              </div>
              {/* Request type row */}
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>{protocolLabel(r.protocol)}</span>
                {r.stream != null && (
                  <span className={`rounded px-1 py-px text-[10px] font-medium ${r.stream ? 'bg-blue-100 text-blue-700' : 'bg-muted text-muted-foreground'}`}>
                    {streamLabel(r.stream)}
                  </span>
                )}
              </div>
              {/* Token row */}
              <div className="flex items-center justify-between text-xs">
                <span className="tabular-nums">
                  {formatTokens(r.inputTokens)} / {formatTokens(r.outputTokens)}
                  {r.cacheTokens != null && r.cacheTokens > 0 && (
                    <span className="text-muted-foreground"> ({formatTokens(r.cacheTokens)}缓存)</span>
                  )}
                </span>
                <span>{formatDuration(r.durationMs)}</span>
              </div>
              {/* Performance row */}
              <div className="flex items-center gap-3 text-xs text-muted-foreground">
                {r.ttftMs != null && r.ttftMs > 0 && r.stream === true && (
                  <span>TTFT {formatDuration(r.ttftMs)}</span>
                )}
                {speed != null && <span>{formatTokensPerSecond(speed)}</span>}
                {(r.thinkingEffectiveEffort ?? r.thinkingEffort) && <span>思考: {thinkingLabel(r.thinkingEffectiveEffort ?? r.thinkingEffort, null, r.thinkingRequestedEffort, r.thinkingEffortDegraded)}</span>}
              </div>
              <p className="text-[10px] text-muted-foreground">{formatTime(r.createdAt)}</p>
            </div>
          );
        })}
        {items.length === 0 && (
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

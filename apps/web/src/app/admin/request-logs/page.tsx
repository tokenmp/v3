'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { adminApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import type { AdminRequestLog } from '@/types/admin';

function statusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function formatUser(log: AdminRequestLog) {
  if (log.userEmail) return log.userEmail;
  if (log.userId) return log.userId.length > 12 ? log.userId.slice(0, 12) + '…' : log.userId;
  return '—';
}

export default function AdminRequestLogsPage() {
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const { data } = useQuery({
    queryKey: ['admin', 'request-logs', page],
    queryFn: () => adminApi.listRequestLogs(page, pageSize),
  });

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="space-y-6">
      {/* Desktop table */}
      <div className="hidden md:block">
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>用户</TableHead>
                <TableHead>模型</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>耗时</TableHead>
                <TableHead>输入 Token</TableHead>
                <TableHead>输出 Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {logs.map((log) => (
                <TableRow
                  key={log.requestId}
                  className="cursor-pointer"
                >
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
                      <Badge variant={statusVariant(log.status)}>
                        {log.status === 'success' ? '成功' : '失败'}
                      </Badge>
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
              {logs.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    暂无请求记录
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {logs.map((log) => (
          <Link key={log.requestId} href={`/admin/request-logs/${log.requestId}`}>
            <Card>
              <CardContent className="p-4 space-y-2">
                <div className="flex items-center justify-between">
                  <span className="font-medium text-sm">{log.model}</span>
                  <Badge variant={statusVariant(log.status)}>
                    {log.status === 'success' ? '成功' : '失败'}
                  </Badge>
                </div>
                <div className="text-xs text-muted-foreground space-y-0.5">
                  <p>用户：{formatUser(log)}</p>
                  <p>时间：{formatTime(log.createdAt)}</p>
                  <p>耗时：{log.durationMs ?? '—'}ms</p>
                  <p>Token：{(log.inputTokens ?? 0).toLocaleString()} / {(log.outputTokens ?? 0).toLocaleString()}</p>
                </div>
              </CardContent>
            </Card>
          </Link>
        ))}
        {logs.length === 0 && (
          <p className="text-center text-muted-foreground py-8 text-sm">暂无请求记录</p>
        )}
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          共 {total} 条
        </p>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
          >
            <ChevronLeft className="h-4 w-4" />
            上一页
          </Button>
          <span className="text-sm">
            第 {page} / {totalPages} 页
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
          >
            下一页
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}

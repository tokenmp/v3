'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { userApi } from '@/lib/api/user';
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
import type { RequestLog } from '@/types';

function statusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function RequestsPage() {
  const [page, setPage] = useState(1);
  const pageSize = 10;

  const { data } = useQuery({
    queryKey: ['requests', page, pageSize],
    queryFn: () => userApi.getRequests(page, pageSize),
  });

  const items = data?.items ?? [];
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
                <TableHead>模型</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>耗时</TableHead>
                <TableHead>输入 Token</TableHead>
                <TableHead>输出 Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r: RequestLog) => (
                <TableRow key={r.requestId}>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(r.createdAt)}
                  </TableCell>
                  <TableCell className="text-sm">{r.model}</TableCell>
                  <TableCell>
                    <Badge variant={statusVariant(r.status)}>{r.status === 'success' ? '成功' : '失败'}</Badge>
                  </TableCell>
                  <TableCell className="text-sm">{r.durationMs ?? '—'}ms</TableCell>
                  <TableCell className="text-sm">{(r.inputTokens ?? 0).toLocaleString()}</TableCell>
                  <TableCell className="text-sm">{(r.outputTokens ?? 0).toLocaleString()}</TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
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
        {items.map((r: RequestLog) => (
          <Card key={r.requestId}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-medium text-sm">{r.model}</span>
                <Badge variant={statusVariant(r.status)}>{r.status === 'success' ? '成功' : '失败'}</Badge>
              </div>
              <div className="text-xs text-muted-foreground space-y-0.5">
                <p>时间：{formatTime(r.createdAt)}</p>
                <p>耗时：{r.durationMs ?? '—'}ms</p>
                <p>Token：{r.inputTokens ?? '—'} / {r.outputTokens ?? '—'}</p>
              </div>
            </CardContent>
          </Card>
        ))}
        {items.length === 0 && (
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

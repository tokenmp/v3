'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { adminApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ArrowLeft } from 'lucide-react';

function statusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function RequestLogDetailPage() {
  const { id } = useParams<{ id: string }>();

  const { data: log, isLoading, error } = useQuery({
    queryKey: ['admin', 'request-log', id],
    queryFn: () => adminApi.getRequestLog(id),
    enabled: !!id,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="h-6 w-6 animate-spin rounded-full border-4 border-primary border-t-transparent" />
      </div>
    );
  }

  if (error || !log) {
    return (
      <div className="space-y-4">
        <Link href="/admin/request-logs">
          <Button variant="ghost" size="sm">
            <ArrowLeft className="h-4 w-4 mr-1" />
            返回列表
          </Button>
        </Link>
        <p className="text-sm text-destructive">加载失败，请稍后重试</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Back button */}
      <Link href="/admin/request-logs">
        <Button variant="ghost" size="sm">
          <ArrowLeft className="h-4 w-4 mr-1" />
          返回列表
        </Button>
      </Link>

      {/* Request summary card */}
      <Card>
        <CardContent className="p-4 sm:p-6">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <div>
              <p className="text-xs text-muted-foreground mb-1">请求 ID</p>
              <p className="text-sm font-mono break-all">{log.requestId}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">模型</p>
              <p className="text-sm">{log.model}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">状态</p>
              <Badge variant={statusVariant(log.status)}>
                {log.status === 'success' ? '成功' : '失败'}
              </Badge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">用户邮箱</p>
              <p className="text-sm">{log.userEmail ?? '—'}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">用户 ID</p>
              <p className="text-sm font-mono break-all">{log.userId ?? '—'}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">时间</p>
              <p className="text-sm">{formatTime(log.createdAt)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">耗时</p>
              <p className="text-sm">{log.durationMs != null ? `${log.durationMs}ms` : '—'}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">输入 Token</p>
              <p className="text-sm">{(log.inputTokens ?? 0).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">输出 Token</p>
              <p className="text-sm">{(log.outputTokens ?? 0).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1">费用</p>
              <p className="text-sm">{log.cost != null ? `¥${log.cost}` : '—'}</p>
            </div>
          </div>
        </CardContent>
      </Card>

    </div>
  );
}

'use client';

import { useQuery } from '@tanstack/react-query';
import { useAuthStore } from '@/lib/auth';
import { userApi } from '@/lib/api/user';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { User, Zap, ShieldCheck } from 'lucide-react';
import type { RequestLog } from '@/types';

function statusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function OverviewPage() {
  const user = useAuthStore((s) => s.user);

  const { data: balance } = useQuery({
    queryKey: ['balance'],
    queryFn: () => userApi.getBalance(),
  });

  const { data: userPlans } = useQuery({
    queryKey: ['userPlans'],
    queryFn: userApi.getUserPlans,
  });

  const { data: recentRequests } = useQuery({
    queryKey: ['recentRequests'],
    queryFn: () => userApi.getRecentRequests(5),
  });

  const activePlan = userPlans?.find((p) => p.status === 'active');
  const total = activePlan ? Number(activePlan.totalQuota) : Number(balance?.tokenRemaining ?? 0);
  const remaining = activePlan ? Number(activePlan.remainingQuota) : Number(balance?.tokenRemaining ?? 0);
  const used = Math.max(0, total - remaining);
  const usedPct = total > 0 ? Math.round((used / total) * 100) : 0;

  return (
    <div className="space-y-6">

      {/* Stat cards */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        {/* Account */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-3 pb-2">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <User className="h-4 w-4" />
            </div>
            <CardTitle className="text-base">账户</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 text-sm">
            <p className="truncate text-muted-foreground">{user?.email ?? '—'}</p>
            <p>
              角色：<span className="text-foreground">{user?.role === 'admin' ? '管理员' : '用户'}</span>
            </p>
            <p>
              注册时间：
              <span className="text-foreground">
                {user?.created_at ? formatTime(user.created_at) : '—'}
              </span>
            </p>
          </CardContent>
        </Card>

        {/* Quota */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-3 pb-2">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <Zap className="h-4 w-4" />
            </div>
            <CardTitle className="text-base">配额</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <p className="text-muted-foreground">{activePlan ? `${activePlan.planType === 'coding' ? '编程' : 'Token'} 套餐` : '—'}</p>
            <div className="flex items-center justify-between text-xs">
              <span>
                已用 {used.toLocaleString()} / {total.toLocaleString()} tokens
              </span>
              <span className="text-muted-foreground">{usedPct}%</span>
            </div>
            <div className="h-2 rounded-full bg-muted overflow-hidden">
              <div
                className="h-full rounded-full bg-primary transition-all"
                style={{ width: `${usedPct}%` }}
              />
            </div>
            {activePlan?.expiresAt && (
              <p className="text-xs text-muted-foreground">到期：{formatTime(activePlan.expiresAt)}</p>
            )}
          </CardContent>
        </Card>

        {/* Status */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-3 pb-2">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="h-4 w-4" />
            </div>
            <CardTitle className="text-base">状态</CardTitle>
          </CardHeader>
          <CardContent>
            <Badge variant="success">正常运行</Badge>
          </CardContent>
        </Card>
      </div>

      {/* Recent requests */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">最近请求</CardTitle>
        </CardHeader>
        <CardContent>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>耗时</TableHead>
                  <TableHead>Token</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(recentRequests ?? []).map((r: RequestLog) => (
                  <TableRow key={r.requestId}>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatTime(r.createdAt)}
                    </TableCell>
                    <TableCell className="text-sm">{r.model}</TableCell>
                    <TableCell>
                      <Badge variant={statusVariant(r.status)}>{r.status === 'success' ? '成功' : '失败'}</Badge>
                    </TableCell>
                    <TableCell className="text-sm">{r.durationMs ?? '—'}ms</TableCell>
                    <TableCell className="text-sm">
                      {r.inputTokens ?? '—'}/{r.outputTokens ?? '—'}
                    </TableCell>
                  </TableRow>
                ))}
                {(!recentRequests || recentRequests.length === 0) && (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                      暂无请求记录
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {(recentRequests ?? []).map((r: RequestLog) => (
              <div
                key={r.requestId}
                className="flex items-center justify-between rounded-lg border p-3"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium truncate">{r.model}</p>
                  <p className="text-xs text-muted-foreground">
                    {formatTime(r.createdAt)} · {r.durationMs ?? '—'}ms
                  </p>
                </div>
                <Badge variant={statusVariant(r.status)}>{r.status === 'success' ? '成功' : '失败'}</Badge>
              </div>
            ))}
            {(!recentRequests || recentRequests.length === 0) && (
              <p className="text-center text-muted-foreground py-8 text-sm">暂无请求记录</p>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

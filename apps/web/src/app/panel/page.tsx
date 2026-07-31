'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { userApi } from '@/lib/api/user';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { buttonVariants } from '@/components/ui/button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Gauge,
  KeyRound,
} from 'lucide-react';
import type { RequestLog, UsageWindow, UserPlan } from '@/types';
import {
  calcTokensPerSecond,
  formatDuration,
  formatTokens,
  formatTokensPerSecond,
  protocolLabel,
  streamLabel,
} from '@/lib/request-log-metrics';

function statusVariant(status: string) {
  if (status === 'success') return 'success' as const;
  if (status === 'processing') return 'secondary' as const;
  if (status === 'cancelled') return 'outline' as const;
  return 'destructive' as const;
}

function statusLabel(status: string) {
  if (status === 'success') return '成功';
  if (status === 'processing') return '处理中';
  if (status === 'cancelled') return '已取消';
  return '失败';
}

function formatTime(iso: string | null | undefined) {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

function formatInt(n: string | number | null | undefined) {
  if (n == null || n === '') return '—';
  const num = typeof n === 'number' ? n : Number(n);
  if (!Number.isFinite(num)) return String(n);
  return Math.round(num).toLocaleString();
}

function toNumber(s: string | number | null | undefined) {
  if (s == null || s === '') return 0;
  const n = typeof s === 'number' ? s : Number(s);
  return Number.isFinite(n) ? n : 0;
}

function usedOf(total: string | number | null | undefined, remaining: string | number | null | undefined) {
  return Math.max(0, toNumber(total) - toNumber(remaining));
}

function pct(used: number, total: string | number | null | undefined) {
  const t = toNumber(total);
  if (t <= 0) return 0;
  return Math.max(0, Math.min(100, (used / t) * 100));
}

function daysLeft(expiresAt: string | null | undefined) {
  if (!expiresAt) return '长期有效';
  const ms = new Date(expiresAt).getTime() - Date.now();
  if (!Number.isFinite(ms)) return '—';
  if (ms <= 0) return '已到期';
  const days = Math.ceil(ms / 86_400_000);
  return `剩余 ${days} 天`;
}

function planName(plan: UserPlan) {
  return plan.planName || (plan.planType === 'coding' ? '编程套餐' : 'Token 套餐');
}

function planTypeLabel(type: string) {
  return type === 'token' ? 'Token 套餐' : '编程套餐';
}

function quotaUnit(type: string) {
  return type === 'token' ? 'tokens' : '次';
}

function windowLabel(scope: UsageWindow['scope']) {
  switch (scope) {
    case 'hour5':
      return '5 小时滚动';
    case 'weekly':
      return '本周额度';
    case 'period':
      return '本周期总额度';
    default:
      return scope;
  }
}

/** Human-readable window reset hint. `windowEnd` (when present) is authoritative;
 * for weekly we annotate the documented Monday 08:00 Beijing reset (UTC Monday
 * 00:00) to help users anticipate the boundary. */
function windowResetHint(w: UsageWindow) {
  if (w.windowEnd) {
    return `重置 ${formatTime(w.windowEnd)}`;
  }
  if (w.scope === 'weekly') return '每周一 08:00 重置（北京时间）';
  if (w.scope === 'hour5') return '5 小时滚动窗口';
  return '';
}

function ProgressBar({ value }: { value: number }) {
  return (
    <div className="h-2 overflow-hidden rounded-full bg-muted">
      <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${value}%` }} />
    </div>
  );
}

function UsageWindowBar({ w, unit }: { w: UsageWindow; unit: string }) {
  const unlimited = w.limit == null;
  const used = Math.max(0, w.consumed);
  const percent = unlimited ? 0 : pct(used, w.limit ?? 0);
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="text-muted-foreground">{windowLabel(w.scope)}</span>
        <span className="text-muted-foreground tabular-nums">
          {unlimited
            ? `已用 ${formatInt(used)} / 不限 ${unit}`
            : `已用 ${formatInt(used)} / ${formatInt(w.limit)} ${unit}`}
        </span>
      </div>
      {unlimited ? (
        <div className="flex h-2 items-center rounded-full bg-muted">
          <div className="h-1 w-full rounded-full bg-primary/40" />
        </div>
      ) : (
        <ProgressBar value={percent} />
      )}
      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
        <span>剩余 {unlimited ? '不限' : `${formatInt(w.remaining)} ${unit}`}</span>
        <span>{windowResetHint(w)}</span>
      </div>
    </div>
  );
}

function PlanCard({ plan }: { plan: UserPlan }) {
  const used = usedOf(plan.totalQuota, plan.remainingQuota);
  const percent = pct(used, plan.totalQuota);
  const unit = quotaUnit(plan.planType);
  return (
    <Card>
      <CardContent className="space-y-4 p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-semibold">{planName(plan)}</h3>
              <Badge variant="success">生效中</Badge>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {planTypeLabel(plan.planType)}{plan.category ? ` · ${plan.category}` : ''}
            </p>
          </div>
          <div className="text-right text-xs text-muted-foreground">
            <div>{daysLeft(plan.expiresAt)}</div>
            <div>{plan.expiresAt ? `到期 ${formatTime(plan.expiresAt)}` : '无到期时间'}</div>
          </div>
        </div>

        {plan.planType === 'coding' && plan.usageWindows && plan.usageWindows.length > 0 ? (
          <div className="space-y-2.5 rounded-md border p-3">
            {(['hour5', 'weekly', 'period'] as const)
              .map((scope) => plan.usageWindows!.find((w) => w.scope === scope))
              .filter((w): w is UsageWindow => Boolean(w))
              .map((w) => (
                <UsageWindowBar key={w.scope} w={w} unit={unit} />
              ))}
          </div>
        ) : (
          <div className="space-y-1.5">
            <div className="flex items-center justify-between text-sm">
              <span className="tabular-nums">已用 {formatInt(used)} / {formatInt(plan.totalQuota)} {unit}</span>
              <span className="text-muted-foreground">{percent.toFixed(percent < 1 && percent > 0 ? 2 : 0)}%</span>
            </div>
            <ProgressBar value={percent} />
            <div className="text-xs text-muted-foreground">剩余 {formatInt(plan.remainingQuota)} {unit}</div>
          </div>
        )}

        {plan.planType === 'coding' ? (
          <div className="grid grid-cols-3 gap-2 text-xs">
            <Limit label="5小时" value={plan.hourlyLimit} unit="次" />
            <Limit label="周" value={plan.weeklyLimit} unit="次" />
            <Limit label="周期" value={plan.monthlyLimit} unit="次" />
          </div>
        ) : (
          <div className="rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
            Token 总额度：{formatInt(plan.tokenLimit ?? plan.totalQuota)} tokens
          </div>
        )}

        <div className="flex items-center justify-between border-t pt-3 text-xs text-muted-foreground">
          <span>生效：{formatTime(plan.activatedAt)}</span>
          <Link href="/panel/requests" className={buttonVariants({ size: 'sm', variant: 'ghost', className: 'h-7 px-2 text-xs' })}>
            查看请求记录
          </Link>
        </div>
      </CardContent>
    </Card>
  );
}

function Limit({ label, value, unit }: { label: string; value?: number | null; unit: string }) {
  return (
    <div className="rounded-md bg-muted/50 px-3 py-2">
      <div className="text-muted-foreground">{label}限制</div>
      <div className="mt-0.5 font-medium tabular-nums">{value == null ? '—' : `${formatInt(value)} ${unit}`}</div>
    </div>
  );
}

function requestSpeed(r: RequestLog) {
  return calcTokensPerSecond({
    outputTokens: r.outputTokens,
    durationMs: r.durationMs,
    ttftMs: r.ttftMs,
    stream: r.stream,
    createdAt: r.createdAt,
    completedAt: r.completedAt,
  });
}

export default function OverviewPage() {
  const { data: userPlans } = useQuery({
    queryKey: ['userPlans'],
    queryFn: userApi.getUserPlans,
  });

  const { data: recentRequests } = useQuery({
    queryKey: ['recentRequests'],
    queryFn: () => userApi.getRecentRequests(5),
  });

  const activePlans = (userPlans ?? []).filter((p) => p.status === 'active');
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">概览</h1>
          <p className="text-sm text-muted-foreground">查看当前套餐和最近调用情况。</p>
        </div>
        <Link href="/panel/settings" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          <KeyRound className="mr-2 h-4 w-4" />计费设置
        </Link>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold">当前套餐</h2>
          <span className="text-xs text-muted-foreground">{activePlans.length} 个生效套餐</span>
        </div>
        {activePlans.length > 0 ? (
          <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
            {activePlans.map((plan) => <PlanCard key={plan.id} plan={plan} />)}
          </div>
        ) : (
          <Card>
            <CardContent className="py-8 text-center text-sm text-muted-foreground">暂无生效套餐</CardContent>
          </Card>
        )}
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-base"><Gauge className="h-4 w-4" />最近请求</CardTitle>
          <Link href="/panel/requests" className={buttonVariants({ variant: 'ghost', size: 'sm' })}>查看全部</Link>
        </CardHeader>
        <CardContent>
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>协议</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">输入</TableHead>
                  <TableHead className="text-right">输出</TableHead>
                  <TableHead className="text-right">速度</TableHead>
                  <TableHead className="text-right">耗时</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(recentRequests ?? []).map((r: RequestLog) => (
                  <TableRow key={r.requestId}>
                    <TableCell className="text-xs text-muted-foreground">{formatTime(r.createdAt)}</TableCell>
                    <TableCell className="text-sm">{r.model || '—'}</TableCell>
                    <TableCell className="text-sm whitespace-nowrap">
                      <span className="inline-flex items-center gap-1.5">
                        <span>{protocolLabel(r.protocol)}</span>
                        {r.stream != null && (
                          <Badge variant={r.stream ? 'info' : 'secondary'} className="rounded px-1 py-px text-[10px]">
                            {streamLabel(r.stream)}
                          </Badge>
                        )}
                      </span>
                    </TableCell>
                    <TableCell><Badge variant={statusVariant(r.status)}>{statusLabel(r.status)}</Badge></TableCell>
                    <TableCell className="text-right tabular-nums">{formatTokens(r.inputTokens)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatTokens(r.outputTokens)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatTokensPerSecond(requestSpeed(r))}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatDuration(r.durationMs)}</TableCell>
                  </TableRow>
                ))}
                {(!recentRequests || recentRequests.length === 0) && (
                  <TableRow>
                    <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">暂无请求记录</TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden space-y-3">
            {(recentRequests ?? []).map((r: RequestLog) => (
              <div key={r.requestId} className="rounded-lg border p-3">
                <div className="flex items-center justify-between gap-2">
                  <p className="min-w-0 truncate text-sm font-medium">{r.model || '—'}</p>
                  <Badge variant={statusVariant(r.status)}>{statusLabel(r.status)}</Badge>
                </div>
                <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                  <span>{protocolLabel(r.protocol)}</span>
                  {r.stream != null && <span>{streamLabel(r.stream)}</span>}
                  <span>· {formatTime(r.createdAt)}</span>
                </div>
                <div className="mt-2 flex justify-between text-xs tabular-nums">
                  <span>{formatTokens(r.inputTokens)} / {formatTokens(r.outputTokens)} tokens</span>
                  <span>{formatDuration(r.durationMs)}</span>
                </div>
              </div>
            ))}
            {(!recentRequests || recentRequests.length === 0) && (
              <p className="py-8 text-center text-sm text-muted-foreground">暂无请求记录</p>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

'use client';

import type { ComponentType } from 'react';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { useAuthStore } from '@/lib/auth';
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
  CalendarClock,
  CircleDollarSign,
  Gauge,
  KeyRound,
  ShieldCheck,
  User,
  Zap,
} from 'lucide-react';
import type { RequestLog, UserPlan } from '@/types';
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

function ProgressBar({ value }: { value: number }) {
  return (
    <div className="h-2 overflow-hidden rounded-full bg-muted">
      <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${value}%` }} />
    </div>
  );
}

function QuotaSummaryCard({
  title,
  icon: Icon,
  remaining,
  used,
  total,
  unit,
  hint,
}: {
  title: string;
  icon: ComponentType<{ className?: string }>;
  remaining: string | number;
  used: number;
  total: string | number;
  unit: string;
  hint: string;
}) {
  const percent = pct(used, total);
  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 pb-2">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Icon className="h-4 w-4" />
        </div>
        <CardTitle className="text-base">{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <div>
          <div className="text-2xl font-semibold tabular-nums">{formatInt(remaining)}</div>
          <div className="text-xs text-muted-foreground">剩余 {unit}</div>
        </div>
        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs">
            <span>已用 {formatInt(used)} / {formatInt(total)} {unit}</span>
            <span className="text-muted-foreground">{percent.toFixed(percent < 1 && percent > 0 ? 2 : 0)}%</span>
          </div>
          <ProgressBar value={percent} />
        </div>
        <p className="text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
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

        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-sm">
            <span className="tabular-nums">已用 {formatInt(used)} / {formatInt(plan.totalQuota)} {unit}</span>
            <span className="text-muted-foreground">{percent.toFixed(percent < 1 && percent > 0 ? 2 : 0)}%</span>
          </div>
          <ProgressBar value={percent} />
          <div className="text-xs text-muted-foreground">剩余 {formatInt(plan.remainingQuota)} {unit}</div>
        </div>

        {plan.planType === 'coding' ? (
          <div className="grid grid-cols-3 gap-2 text-xs">
            <Limit label="小时" value={plan.hourlyLimit} unit="次" />
            <Limit label="周" value={plan.weeklyLimit} unit="次" />
            <Limit label="月" value={plan.monthlyLimit} unit="次" />
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

  const activePlans = (userPlans ?? []).filter((p) => p.status === 'active');
  const codingPlans = activePlans.filter((p) => p.planType === 'coding');
  const tokenPlans = activePlans.filter((p) => p.planType === 'token');
  const codingTotal = codingPlans.reduce((sum, p) => sum + toNumber(p.totalQuota), 0);
  const tokenTotal = tokenPlans.reduce((sum, p) => sum + toNumber(p.totalQuota), 0);
  const codingRemaining = toNumber(balance?.codingRemaining ?? codingPlans[0]?.remainingQuota ?? 0);
  const tokenRemaining = toNumber(balance?.tokenRemaining ?? tokenPlans[0]?.remainingQuota ?? 0);
  const codingUsed = Math.max(0, codingTotal - codingRemaining);
  const tokenUsed = Math.max(0, tokenTotal - tokenRemaining);
  const preferred = '编程额度';

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">概览</h1>
          <p className="text-sm text-muted-foreground">查看当前套餐、额度余额和最近调用情况。</p>
        </div>
        <Link href="/panel/settings" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          <KeyRound className="mr-2 h-4 w-4" />计费设置
        </Link>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader className="flex flex-row items-center gap-3 pb-2">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <User className="h-4 w-4" />
            </div>
            <CardTitle className="text-base">账户</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 text-sm">
            <p className="truncate text-muted-foreground">{user?.email ?? '—'}</p>
            <p>角色：<span className="text-foreground">{user?.role === 'admin' ? '管理员' : '用户'}</span></p>
            <p>注册时间：<span className="text-foreground">{user?.created_at ? formatTime(user.created_at) : '—'}</span></p>
          </CardContent>
        </Card>

        <QuotaSummaryCard
          title="编程请求额度"
          icon={Zap}
          remaining={codingRemaining}
          used={codingUsed}
          total={codingTotal}
          unit="次"
          hint="成功模型请求按 1 次扣除 · 本月周期"
        />

        <QuotaSummaryCard
          title="Token 余额"
          icon={CircleDollarSign}
          remaining={tokenRemaining}
          used={tokenUsed}
          total={tokenTotal}
          unit="tokens"
          hint="Token 套餐按余额叠加 · 长期额度"
        />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_280px]">
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

        <div className="space-y-3">
          <h2 className="text-base font-semibold">计费状态</h2>
          <Card>
            <CardContent className="space-y-4 p-4 text-sm">
              <div className="flex items-center gap-3">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-green-100 text-green-700">
                  <ShieldCheck className="h-4 w-4" />
                </div>
                <div>
                  <div className="font-medium">账户正常</div>
                  <div className="text-xs text-muted-foreground">可继续调用模型</div>
                </div>
              </div>
              <div className="grid gap-2 border-t pt-3 text-xs">
                <div className="flex justify-between"><span className="text-muted-foreground">当前优先</span><span>{preferred}</span></div>
                <div className="flex justify-between"><span className="text-muted-foreground">Coding 套餐</span><span>{codingPlans.length} 个</span></div>
                <div className="flex justify-between"><span className="text-muted-foreground">Token 套餐</span><span>{tokenPlans.length} 个</span></div>
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="space-y-3 p-4 text-sm">
              <div className="flex items-center gap-2 font-medium"><CalendarClock className="h-4 w-4" />最近到期</div>
              {activePlans.filter((p) => p.expiresAt).sort((a, b) => new Date(a.expiresAt ?? '').getTime() - new Date(b.expiresAt ?? '').getTime()).slice(0, 2).map((p) => (
                <div key={p.id} className="rounded-md bg-muted/50 p-3 text-xs">
                  <div className="font-medium">{planName(p)}</div>
                  <div className="mt-1 text-muted-foreground">{formatTime(p.expiresAt)} · {daysLeft(p.expiresAt)}</div>
                </div>
              ))}
              {activePlans.every((p) => !p.expiresAt) && <div className="text-xs text-muted-foreground">暂无即将到期套餐</div>}
            </CardContent>
          </Card>
        </div>
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
                          <span className={`rounded px-1 py-px text-[10px] font-medium ${r.stream ? 'bg-blue-100 text-blue-700' : 'bg-muted text-muted-foreground'}`}>
                            {streamLabel(r.stream)}
                          </span>
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

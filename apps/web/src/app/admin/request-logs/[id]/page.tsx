'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { adminApi } from '@/lib/api/admin';
import type { RequestLogAttempt } from '@/types';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { copyText } from '@/lib/utils';
import {
  ArrowDownToLine,
  ArrowLeft,
  ArrowUpFromLine,
  Calendar,
  Check,
  Clock,
  Copy,
  Cpu,
  Layers,
  Radio,
  Server,
  Zap,
  AlertTriangle,
} from 'lucide-react';

/* -------------------------------------------------------------------------- */
/* Helpers                                                                     */
/* -------------------------------------------------------------------------- */

function formatTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', { hour12: false });
}

function formatMs(ms: number | null | undefined): string {
  if (ms == null) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function protocolLabel(p: string | null | undefined): string {
  switch (p) {
    case 'openai_chat':
      return 'OpenAI Chat';
    case 'anthropic_messages':
      return 'Anthropic Messages';
    case 'openai_responses':
      return 'OpenAI Responses';
    case 'openai_images':
      return 'OpenAI Images';
    default:
      return p ?? '—';
  }
}

function attemptStatus(a: RequestLogAttempt): 'success' | 'error' {
  return String(a.status ?? a.final_status ?? '') === 'success' ? 'success' : 'error';
}

function retryStopLabel(stop: string): string {
  switch (stop) {
    case 'no_match': return '无匹配规则';
    case 'retry_none': return '规则禁止';
    case 'max_total_attempts': return '达总次数上限';
    case 'max_same_target_attempts': return '达同目标上限';
    case 'max_total_duration': return '达总时长上限';
    case 'deadline': return '超时截止';
    case 'no_candidate': return '无候选';
    case 'committed': return '已提交';
    case 'canceled': return '已取消';
    case 'unclassified': return '未分类错误';
    default: return stop || '—';
  }
}

/* -------------------------------------------------------------------------- */
/* KPI stat tile                                                               */
/* -------------------------------------------------------------------------- */

function StatTile({
  icon: Icon,
  label,
  value,
  hint,
  tone,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
  hint?: string;
  tone: 'blue' | 'green' | 'orange' | 'purple';
}) {
  const tones = {
    blue: 'bg-blue-500/10 text-blue-600 dark:text-blue-400',
    green: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    orange: 'bg-amber-500/10 text-amber-600 dark:text-amber-400',
    purple: 'bg-violet-500/10 text-violet-600 dark:text-violet-400',
  } as const;
  return (
    <Card>
      <CardContent className="flex items-center gap-3 p-4">
        <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${tones[tone]}`}>
          <Icon className="h-5 w-5" />
        </span>
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{label}</p>
          <p className="text-lg font-semibold leading-tight">{value}</p>
          {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
        </div>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Info row (label / value)                                                    */
/* -------------------------------------------------------------------------- */

function InfoRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-0.5 py-2.5 sm:flex-row sm:items-center sm:gap-4 sm:py-2">
      <dt className="w-32 shrink-0 text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="text-sm min-w-0">{children}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Attempt timeline node                                                       */
/* -------------------------------------------------------------------------- */

function AttemptNode({ a, isLast }: { a: RequestLogAttempt; isLast: boolean }) {
  const ok = attemptStatus(a);
  const idx = a.attemptIndex ?? a.attempt_index;
  const provider = a.providerId ?? a.provider_id ?? a.provider;
  const upstreamModel = a.upstreamModel ?? a.upstream_model;
  const httpStatus = a.httpStatus ?? a.http_status;
  const upstreamHttpStatus = a.upstreamHttpStatus ?? a.upstream_http_status ?? a.upstreamHttpStatus;
  const latency = a.latencyMs ?? a.latency_ms;
  const errorCode = a.errorCode ?? a.error_code;
  const errorType = a.errorType ?? a.error_type;
  const retryClassified = a.retryClassified ?? a.retry_classified;
  const metadata = a.metadata as Record<string, string> | undefined;
  const upstreamMessage = metadata?.upstream_message ?? metadata?.upstreamMessage;
  const createdAt = a.created_at ?? a.createdAt;

  return (
    <li className="relative pl-8">
      {/* timeline rail + dot */}
      {!isLast && (
        <span className="absolute left-[11px] top-6 bottom-0 w-px bg-border" aria-hidden />
      )}
      <span
        className={`absolute left-1.5 top-1.5 flex h-4 w-4 items-center justify-center rounded-full ring-4 ring-background ${
          ok === 'success' ? 'bg-emerald-500' : 'bg-destructive'
        }`}
        aria-hidden
      />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-sm font-semibold">#{String(idx ?? '?')}</span>
        {provider ? <span className="text-sm text-muted-foreground">{String(provider)}</span> : null}
        {upstreamModel ? (
          <span className="inline-flex items-center gap-1 text-sm">
            <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
            {String(upstreamModel)}
          </span>
        ) : null}
        <Badge variant={ok === 'success' ? 'success' : 'destructive'}>
          {ok === 'success' ? '成功' : '失败'}
        </Badge>
        {latency != null && (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <Clock className="h-3.5 w-3.5" />
            {formatMs(Number(latency))}
          </span>
        )}
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
        {httpStatus != null && Number(httpStatus) > 0 && <span>HTTP {String(httpStatus)}</span>}
        {upstreamHttpStatus != null && Number(upstreamHttpStatus) > 0 && (
          <span className="font-medium text-amber-600 dark:text-amber-500">上游 {String(upstreamHttpStatus)}</span>
        )}
        {errorCode ? <span className="text-destructive">err: {String(errorCode)}</span> : null}
        {errorType && errorType !== errorCode ? <span>type: {String(errorType)}</span> : null}
        {retryClassified ? (
          <Badge variant="outline" className="text-[10px] py-0 px-1.5">重试: {retryStopLabel(String(retryClassified))}</Badge>
        ) : null}
        {createdAt ? <span>{formatTime(String(createdAt))}</span> : null}
      </div>
      {upstreamMessage ? (
        <div className="mt-1 rounded bg-muted/50 px-2 py-1 text-xs text-muted-foreground">
          上游错误：{String(upstreamMessage)}
        </div>
      ) : null}
    </li>
  );
}

/* -------------------------------------------------------------------------- */
/* Page                                                                         */
/* -------------------------------------------------------------------------- */

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

  const isSuccess = log.status === 'success';
  const attempts = log.attempts ?? [];
  const hasError = !!log.errorMessage || !!log.errorCode;

  return (
    <div className="space-y-6">
      {/* Back */}
      <Link href="/admin/request-logs">
        <Button variant="ghost" size="sm">
          <ArrowLeft className="h-4 w-4 mr-1" />
          返回列表
        </Button>
      </Link>

      {/* Header banner */}
      <Card className={isSuccess ? '' : 'border-destructive/40'}>
        <CardContent className="flex flex-col gap-4 p-4 sm:p-6">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="space-y-1.5 min-w-0">
              <div className="flex items-center gap-2.5">
                <h1 className="text-lg font-semibold">请求详情</h1>
                <Badge variant={isSuccess ? 'success' : 'destructive'}>
                  {isSuccess ? '成功' : '失败'}
                </Badge>
              </div>
              <div className="flex items-center gap-1.5">
                <span className="font-mono text-sm text-muted-foreground break-all">{log.requestId}</span>
                <CopyButton text={log.requestId} />
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-1.5 text-sm text-muted-foreground">
              <Calendar className="h-4 w-4" />
              {formatTime(log.createdAt)}
            </div>
          </div>
        </CardContent>
      </Card>

      {/* KPI stats */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile icon={Clock} label="总耗时" value={formatMs(log.durationMs)} tone="blue" />
        <StatTile
          icon={ArrowDownToLine}
          label="输入 Token"
          value={(log.inputTokens ?? 0).toLocaleString()}
          tone="green"
        />
        <StatTile
          icon={ArrowUpFromLine}
          label="输出 Token"
          value={(log.outputTokens ?? 0).toLocaleString()}
          tone="orange"
        />
        <StatTile
          icon={Zap}
          label="总 Token"
          value={(log.totalTokens ?? (log.inputTokens ?? 0) + (log.outputTokens ?? 0)).toLocaleString()}
          tone="purple"
        />
      </div>

      {/* Error banner */}
      {hasError && (
        <Card className="border-destructive/40">
          <CardContent className="flex items-start gap-3 p-4">
            <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
            <div className="min-w-0 space-y-1">
              <p className="text-sm font-medium text-destructive">请求失败</p>
              {log.errorCode && (
                <p className="text-xs text-muted-foreground">
                  错误码：<span className="font-mono">{log.errorCode}</span>
                  {log.errorType ? ` · 类型：${log.errorType}` : ''}
                  {log.upstreamHttpStatus ? ` · 上游 HTTP ${log.upstreamHttpStatus}` : ''}
                </p>
              )}
              {log.errorMessage && (
                <p className="text-sm break-all">{log.errorMessage}</p>
              )}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Request info */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base">请求信息</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          <dl className="divide-y divide-border">
            <InfoRow label="用户邮箱">
              <span>{log.userEmail ?? '—'}</span>
            </InfoRow>
            <div className="flex flex-col gap-1 py-2.5 sm:flex-row sm:items-center sm:gap-4">
              <dt className="w-32 shrink-0 text-xs font-medium text-muted-foreground">用户 ID</dt>
              <dd>
                <span className="flex items-center gap-1.5">
                  <span className="font-mono text-sm break-all">{log.userId ?? '—'}</span>
                  {log.userId && <CopyButton text={log.userId} />}
                </span>
              </dd>
            </div>
            <InfoRow label="模型">
              <span className="inline-flex items-center gap-1.5">
                <Cpu className="h-4 w-4 text-muted-foreground" />
                {log.model || '—'}
              </span>
            </InfoRow>
            <InfoRow label="Provider">
              <span className="inline-flex items-center gap-1.5">
                <Server className="h-4 w-4 text-muted-foreground" />
                {log.provider ?? '—'}
              </span>
            </InfoRow>
            <InfoRow label="协议">
              <span className="inline-flex items-center gap-1.5">
                <Layers className="h-4 w-4 text-muted-foreground" />
                {protocolLabel(log.protocol)}
              </span>
            </InfoRow>
            <InfoRow label="流式">
              <span className="inline-flex items-center gap-1.5">
                <Radio className="h-4 w-4 text-muted-foreground" />
                {log.stream == null ? '—' : log.stream ? '是' : '否'}
              </span>
            </InfoRow>
            <InfoRow label="计费套餐">
              {log.billingPlan ?? '—'}
            </InfoRow>
          </dl>
        </CardContent>
      </Card>

      {/* Attempts timeline */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center justify-between text-base">
            <span>尝试记录</span>
            <Badge variant="secondary">{attempts.length} 次</Badge>
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {attempts.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">暂无尝试记录</p>
          ) : (
            <ul className="space-y-4 py-2">
              {attempts.map((a, i) => (
                <AttemptNode key={i} a={a} isLast={i === attempts.length - 1} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Inline copy button (compact, for header/rows)                               */
/* -------------------------------------------------------------------------- */

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        if (await copyText(text)) {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }
      }}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      title="复制"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
}

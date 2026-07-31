'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { adminApi } from '@/lib/api/admin';
import type { RequestLogAttempt, RequestLogEvent } from '@/types';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { copyText } from '@/lib/utils';
import {
  formatDuration,
  formatTokens,
  formatTokensPerSecond,
  calcTokensPerSecond,
  protocolLabelFull,
  streamLabel,
  thinkingLabel,
} from '@/lib/request-log-metrics';
import {
  ArrowLeft,
  Calendar,
  Check,
  Clock,
  Copy,
  Cpu,
  KeyRound,
  Layers,
  Radio,
  Server,
  Route as RouteIcon,
  Link2,
  Zap,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  Timer,
  Activity,
  Gauge,
  Globe,
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

function statusLabel(s: string | null | undefined): { label: string; tone: 'success' | 'destructive' | 'secondary' } {
  switch (s) {
    case 'success':
      return { label: '成功', tone: 'success' };
    case 'processing':
      return { label: '处理中', tone: 'secondary' };
    case 'upstream_error':
      return { label: '上游错误', tone: 'destructive' };
    case 'timeout':
      return { label: '超时', tone: 'destructive' };
    case 'transport_error':
      return { label: '传输错误', tone: 'destructive' };
    case 'client_error':
      return { label: '客户端错误', tone: 'destructive' };
    case 'client_cancelled':
    case 'cancelled':
      return { label: '客户端取消', tone: 'destructive' };
    default:
      return { label: s ?? '未知', tone: 'secondary' };
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

function stageLabel(stage: string): string {
  switch (stage) {
    case 'received': return '收到请求';
    case 'key_verified': return '密钥验证';
    case 'route_selected': return '路由选择';
    case 'quota_reserved': return '配额预留';
    case 'upstream_started': return '上游开始';
    case 'upstream_finished': return '上游完成';
    case 'terminal': return '终态';
    case 'completed': return '完成';
    default: return stage;
  }
}

function eventStatusIcon(status: string): { icon: React.ComponentType<{ className?: string }>; color: string } {
  switch (status) {
    case 'success':
      return { icon: CheckCircle2, color: 'text-emerald-500' };
    case 'failed':
      return { icon: XCircle, color: 'text-destructive' };
    case 'skipped':
      return { icon: CheckCircle2, color: 'text-muted-foreground' };
    default:
      return { icon: Activity, color: 'text-blue-500' };
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

function InfoRowWithCopy({ label, value }: { label: string; value: string | null | undefined }) {
  return (
    <InfoRow label={label}>
      {value ? (
        <span className="flex items-center gap-1.5">
          <span className="font-mono text-sm break-all">{value}</span>
          <CopyButton text={value} />
        </span>
      ) : (
        <span className="text-muted-foreground">—</span>
      )}
    </InfoRow>
  );
}

/* -------------------------------------------------------------------------- */
/* Attempt timeline node                                                       */
/* -------------------------------------------------------------------------- */

function AttemptNode({ a, isLast }: { a: RequestLogAttempt; isLast: boolean }) {
  const ok = attemptStatus(a);
  const idx = a.attemptIndex ?? a.attempt_index;
  const provider = a.providerId ?? a.provider_id ?? a.provider;
  const routeId = a.routeId ?? a.route_id;
  const credentialId = a.credentialId ?? a.credential_id;
  const upstreamModel = a.upstreamModel ?? a.upstream_model;
  const upstreamUrl = a.upstreamUrl ?? a.upstream_url;
  const httpStatus = a.httpStatus ?? a.http_status;
  const upstreamHttpStatus = a.upstreamHttpStatus ?? a.upstream_http_status ?? a.upstreamHttpStatus;
  const latency = a.latencyMs ?? a.latency_ms;
  const errorCode = a.errorCode ?? a.error_code;
  const errorType = a.errorType ?? a.error_type;
  const retryClassified = a.retryClassified ?? a.retry_classified;
  const metadata = a.metadata as Record<string, string> | undefined;
  const retryStop = metadata?.retry_stop ?? metadata?.retryStop;
  const createdAt = a.created_at ?? a.createdAt;

  return (
    <li className="relative pl-8">
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
        {provider ? (
          <span className="inline-flex items-center gap-1 text-sm text-muted-foreground">
            <Server className="h-3.5 w-3.5" />
            {String(provider)}
          </span>
        ) : null}
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
            {formatDuration(Number(latency))}
          </span>
        )}
      </div>
      {/* routing/credential detail */}
      <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {routeId ? (
          <span className="inline-flex items-center gap-1">
            <RouteIcon className="h-3 w-3" />
            路由：<span className="font-mono">{String(routeId)}</span>
          </span>
        ) : null}
        {credentialId ? (
          <span className="inline-flex items-center gap-1">
            <KeyRound className="h-3 w-3" />
            凭据：<span className="font-mono">{String(credentialId)}</span>
          </span>
        ) : null}
        {upstreamUrl ? (
          <span className="inline-flex items-center gap-1 min-w-0">
            <Link2 className="h-3 w-3 shrink-0" />
            <span className="font-mono truncate" title={String(upstreamUrl)}>{String(upstreamUrl)}</span>
          </span>
        ) : null}
      </div>
      {/* error detail */}
      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
        {httpStatus != null && Number(httpStatus) > 0 && <span>HTTP {String(httpStatus)}</span>}
        {upstreamHttpStatus != null && Number(upstreamHttpStatus) > 0 && (
          <span className="font-medium text-amber-600 dark:text-amber-500">上游 {String(upstreamHttpStatus)}</span>
        )}
        {errorCode ? <span className="text-destructive">err: {String(errorCode)}</span> : null}
        {errorType && errorType !== errorCode ? <span>type: {String(errorType)}</span> : null}
        {retryStop ? (
          <Badge variant="outline" className="text-[10px] py-0 px-1.5">重试: {retryStopLabel(String(retryStop))}</Badge>
        ) : retryClassified ? (
          <Badge variant="outline" className="text-[10px] py-0 px-1.5">重试: {String(retryClassified)}</Badge>
        ) : null}
        {createdAt ? <span>{formatTime(String(createdAt))}</span> : null}
      </div>
    </li>
  );
}

/* -------------------------------------------------------------------------- */
/* Event timeline node (trace)                                                 */
/* -------------------------------------------------------------------------- */

function EventNode({ e, isLast }: { e: RequestLogEvent; isLast: boolean }) {
  const stage = String(e.stage ?? '');
  const status = String(e.status ?? 'info');
  const source = String(e.source ?? '');
  const { icon: Icon, color } = eventStatusIcon(status);
  const createdAt = e.created_at ?? e.createdAt;
  const duration = e.duration_ms ?? e.durationMs;
  const message = e.message;
  const attemptIdx = e.attempt_index ?? e.attemptIndex;

  return (
    <li className="relative pl-8">
      {!isLast && (
        <span className="absolute left-[11px] top-5 bottom-0 w-px bg-border" aria-hidden />
      )}
      <Icon className={`absolute left-0 top-0.5 h-4 w-4 ${color}`} aria-hidden />
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="text-sm font-medium">{stageLabel(stage)}</span>
        {source && (
          <Badge variant="outline" className="text-[10px] py-0 px-1.5">
            {source === 'edge' ? 'Edge' : source === 'executor' ? 'Executor' : source}
          </Badge>
        )}
        {attemptIdx != null && (
          <Badge variant="secondary" className="text-[10px] py-0 px-1.5">#{String(attemptIdx)}</Badge>
        )}
        {duration != null && Number(duration) > 0 && (
          <span className="inline-flex items-center gap-0.5 text-xs text-muted-foreground">
            <Timer className="h-3 w-3" />
            {formatDuration(Number(duration))}
          </span>
        )}
        {createdAt ? (
          <span className="text-xs text-muted-foreground">{formatTime(String(createdAt))}</span>
        ) : null}
      </div>
      {message ? (
        <p className="mt-0.5 text-xs text-muted-foreground break-all">{String(message)}</p>
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
    // Poll while processing so the event timeline, TTFT and completion time
    // appear without a manual refresh; stop once a terminal status arrives.
    refetchInterval: (query) => query.state.data?.status === 'processing' ? 1_000 : false,
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
  const isProcessing = log.status === 'processing';
  const attempts = log.attempts ?? [];
  const events = log.events ?? [];
  const hasError = !isProcessing && (!!log.errorMessage || !!log.errorCode);
  const st = statusLabel(isProcessing ? 'processing' : (isSuccess ? 'success' : (log.errorType ?? 'error')));

  // Compute tokens/s for KPI
  const tokensPerSec = calcTokensPerSecond({
    outputTokens: log.outputTokens,
    durationMs: log.durationMs,
    ttftMs: log.ttftMs,
    stream: log.stream,
    createdAt: log.createdAt,
    completedAt: log.completedAt,
  });

  // Total tokens: prefer explicit field, fallback to sum
  const totalTokens = log.totalTokens ?? ((log.inputTokens ?? 0) + (log.outputTokens ?? 0));

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
      <Card className={isSuccess || isProcessing ? '' : 'border-destructive/40'}>
        <CardContent className="flex flex-col gap-4 p-4 sm:p-6">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="space-y-1.5 min-w-0">
              <div className="flex items-center gap-2.5">
                <h1 className="text-lg font-semibold">请求详情</h1>
                <Badge variant={st.tone}>{st.label}</Badge>
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

      {/* KPI stats — show for successful and processing requests (failed requests show error banner instead) */}
      {(isSuccess || isProcessing) && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatTile icon={Clock} label="总耗时" value={formatDuration(log.durationMs)} tone="blue" />
          {log.ttftMs != null && log.ttftMs > 0 && log.stream === true && (
            <StatTile icon={Zap} label="首字耗时 (TTFT)" value={formatDuration(log.ttftMs)} tone="purple" />
          )}
          <StatTile
            icon={Activity}
            label="Token (入/出)"
            value={`${formatTokens(log.inputTokens)} / ${formatTokens(log.outputTokens)}`}
            hint={log.cacheTokens != null && log.cacheTokens > 0 ? `缓存 ${formatTokens(log.cacheTokens)} · 总计 ${formatTokens(totalTokens)}` : `总计 ${formatTokens(totalTokens)}`}
            tone="green"
          />
          {tokensPerSec != null && (
            <StatTile icon={Gauge} label="生成速度" value={formatTokensPerSecond(tokensPerSec)} tone="orange" />
          )}
        </div>
      )}

      {/* Error banner — only for failed requests */}
      {hasError && (
        <Card className="border-destructive/40">
          <CardContent className="flex items-start gap-3 p-4">
            <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
            <div className="min-w-0 space-y-1.5">
              <p className="text-sm font-medium text-destructive">请求失败</p>
              {log.errorCode && (
                <p className="text-xs text-muted-foreground">
                  错误码：<span className="font-mono">{log.errorCode}</span>
                  {log.errorType ? ` · 类型：${log.errorType}` : ''}
                  {log.upstreamHttpStatus ? ` · 上游 HTTP ${log.upstreamHttpStatus}` : ''}
                  {log.httpStatus ? ` · 返回 HTTP ${log.httpStatus}` : ''}
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
            <InfoRowWithCopy label="用户 ID" value={log.userId} />
            {log.clientKeyId ? (
              <InfoRowWithCopy label="客户端密钥" value={log.clientKeyId} />
            ) : null}
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
            {log.routeId ? (
              <InfoRow label="路由">
                <span className="inline-flex items-center gap-1.5">
                  <RouteIcon className="h-4 w-4 text-muted-foreground" />
                  <span className="font-mono text-sm">{log.routeId}</span>
                </span>
              </InfoRow>
            ) : null}
            {log.credentialId ? (
              <InfoRow label="凭据">
                <span className="inline-flex items-center gap-1.5">
                  <KeyRound className="h-4 w-4 text-muted-foreground" />
                  <span className="font-mono text-sm">{log.credentialId}</span>
                </span>
              </InfoRow>
            ) : null}
            <InfoRow label="协议">
              <span className="inline-flex items-center gap-1.5">
                <Layers className="h-4 w-4 text-muted-foreground" />
                {protocolLabelFull(log.protocol)}
              </span>
            </InfoRow>
            <InfoRow label="流式">
              <span className="inline-flex items-center gap-1.5">
                <Radio className="h-4 w-4 text-muted-foreground" />
                {streamLabel(log.stream)}
              </span>
            </InfoRow>
            {(log.thinkingMode || log.thinkingEffort) ? (
              <InfoRow label="思考">
                <span>{thinkingLabel(log.thinkingEffectiveEffort ?? log.thinkingEffort, log.thinkingMode, log.thinkingRequestedEffort, log.thinkingEffortDegraded)}</span>
              </InfoRow>
            ) : null}
            <InfoRow label="计费套餐">
              {log.billingPlan ?? '—'}
            </InfoRow>
            <InfoRow label="开始时间">
              <span>{formatTime(log.createdAt)}</span>
            </InfoRow>
            {log.completedAt ? (
              <InfoRow label="完成时间">
                <span>{formatTime(log.completedAt)}</span>
              </InfoRow>
            ) : null}
            {/* User-Agent — full value, copyable */}
            <InfoRow label="User-Agent">
              {log.userAgent ? (
                <span className="flex items-start gap-1.5">
                  <Globe className="h-4 w-4 shrink-0 text-muted-foreground mt-0.5" />
                  <span className="font-mono text-sm break-all whitespace-pre-wrap">{log.userAgent}</span>
                  <CopyButton text={log.userAgent} />
                </span>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
            </InfoRow>
          </dl>
        </CardContent>
      </Card>

      {/* Events trace timeline */}
      {events.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center justify-between text-base">
              <span>事件时间线</span>
              <Badge variant="secondary">{events.length} 条</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <ul className="space-y-3 py-2">
              {events.map((e, i) => (
                <EventNode key={i} e={e} isLast={i === events.length - 1} />
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

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

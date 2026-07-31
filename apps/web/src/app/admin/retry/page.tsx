'use client';

import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import {
  ArrowDownToLine,
  GripVertical,
  Plus,
  Save,
  Trash2,
  Zap,
} from 'lucide-react';
import { adminConfigApi } from '@/lib/api/admin';
import type {
  AdminRouteConfig,
  RoutingPolicy,
  RetryAction,
  RetryPolicy,
  RetryRule,
} from '@/types/admin';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { PublishStatusHint } from '@/components/publish-status-hint';
import { PageHeader } from '@/components/page-header';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Field } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

/* -------------------------------------------------------------------------- */
/* Constants                                                                    */
/* -------------------------------------------------------------------------- */

const ACTION_OPTIONS: { value: RetryAction; label: string; hint: string }[] = [
  { value: 'none', label: '不重试', hint: '终止重试' },
  { value: 'same_credential', label: '同目标重试', hint: '同一 route/credential 再试一次（适用 503 瞬时过载）' },
  { value: 'next_credential', label: '换密钥', hint: '同路由下另一个 credential（适用 429 限流）' },
  { value: 'next_route', label: '换路由', hint: '同模型另一路由（适用 5xx 上游故障）' },
  { value: 'next_provider', label: '换 Provider', hint: '同模型不同 provider' },
  { value: 'next_model', label: '换模型', hint: 'fallback 到另一个模型' },
];

function actionLabel(a: RetryAction): string {
  return ACTION_OPTIONS.find((o) => o.value === a)?.label ?? a;
}

/* -------------------------------------------------------------------------- */
/* Retry rule editor (one row)                                                  */
/* -------------------------------------------------------------------------- */

function RetryRuleEditor({
  rule,
  onChange,
  onRemove,
}: {
  rule: RetryRule;
  onChange: (r: RetryRule) => void;
  onRemove: () => void;
}) {
  const statusInput = rule.httpStatuses.join(', ');
  return (
    <div className="grid grid-cols-1 gap-3 rounded-lg border p-3 sm:grid-cols-[auto_1fr_1fr_1fr_auto] sm:items-end">
      <div className="hidden sm:block">
        <GripVertical className="h-4 w-4 text-muted-foreground" />
      </div>
      <Field label="规则 ID">
        <Input
          value={rule.id}
          onChange={(e) => onChange({ ...rule, id: e.target.value })}
          placeholder="retry-429"
        />
      </Field>
      <Field label="HTTP 状态码（逗号分隔）">
        <Input
          value={statusInput}
          onChange={(e) =>
            onChange({
              ...rule,
              httpStatuses: e.target.value
                .split(',')
                .map((s) => Number(s.trim()))
                .filter((n) => !Number.isNaN(n) && n > 0),
            })
          }
          placeholder="429 或 500,502,503"
        />
      </Field>
      <Field label="动作">
        <select
          className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
          value={rule.action}
          onChange={(e) => onChange({ ...rule, action: e.target.value as RetryAction })}
        >
          {ACTION_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="优先级">
        <Input
          type="number"
          value={rule.priority}
          onChange={(e) => onChange({ ...rule, priority: Number(e.target.value) })}
        />
      </Field>
      <div className="flex sm:pb-1">
        <Button variant="ghost" size="sm" onClick={onRemove} title="删除规则">
          <Trash2 className="h-4 w-4 text-destructive" />
        </Button>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Retry policy editor (shared by global + route)                              */
/* -------------------------------------------------------------------------- */

function emptyRule(): RetryRule {
  return { id: `rule-${Date.now() % 100000}`, priority: 10, httpStatuses: [429], action: 'next_credential' };
}

function RetryPolicyEditor({
  policy,
  onChange,
}: {
  policy: RetryPolicy;
  onChange: (p: RetryPolicy) => void;
}) {
  const rules = policy.rules ?? [];
  const updateRule = (i: number, r: RetryRule) => {
    const next = [...rules];
    next[i] = r;
    onChange({ ...policy, rules: next });
  };
  const addRule = () => onChange({ ...policy, rules: [...rules, emptyRule()] });
  const removeRule = (i: number) =>
    onChange({ ...policy, rules: rules.filter((_, idx) => idx !== i) });

  return (
    <div className="space-y-4">
      {/* params */}
      <div className="grid grid-cols-2 gap-3">
        <Field label="最大尝试次数">
          <Input
            type="number"
            value={policy.maxTotalAttempts ?? ''}
            onChange={(e) =>
              onChange({ ...policy, maxTotalAttempts: e.target.value === '' ? null : Number(e.target.value) })
            }
            placeholder="3"
          />
        </Field>
        <Field label="同目标最大尝试">
          <Input
            type="number"
            value={policy.maxSameTargetAttempts ?? ''}
            onChange={(e) =>
              onChange({ ...policy, maxSameTargetAttempts: e.target.value === '' ? null : Number(e.target.value) })
            }
            placeholder="2"
          />
        </Field>
        <Field label="退避间隔">
          <Input
            value={policy.backoff ?? ''}
            onChange={(e) => onChange({ ...policy, backoff: e.target.value || undefined })}
            placeholder="500ms"
          />
        </Field>
        <Field label="最大总时长">
          <Input
            value={policy.maxTotalDuration ?? ''}
            onChange={(e) => onChange({ ...policy, maxTotalDuration: e.target.value || undefined })}
            placeholder="45s"
          />
        </Field>
      </div>

      {/* rules */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h4 className="text-sm font-medium">规则列表</h4>
          <Button variant="outline" size="sm" onClick={addRule}>
            <Plus className="h-4 w-4 mr-1" />
            添加规则
          </Button>
        </div>
        {rules.length === 0 ? (
          <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
            暂无规则。空规则表示不重试（继承上层策略）。
          </p>
        ) : (
          <div className="space-y-3">
            {rules.map((r, i) => (
              <RetryRuleEditor
                key={i}
                rule={r}
                onChange={(nr) => updateRule(i, nr)}
                onRemove={() => removeRule(i)}
              />
            ))}
          </div>
        )}
      </div>

      {/* quick presets */}
      <div className="flex flex-col sm:flex-row flex-wrap gap-2 rounded-lg bg-muted/40 p-3">
        <span className="text-xs text-muted-foreground self-center mr-1">快速模板：</span>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              onChange({
                maxTotalAttempts: 3,
                maxSameTargetAttempts: 2,
                backoff: '500ms',
                maxTotalDuration: '45s',
                rules: [
                  { id: 'retry-429', priority: 10, httpStatuses: [429], action: 'next_credential' },
                  { id: 'retry-5xx', priority: 20, httpStatuses: [500, 502, 503, 504], action: 'next_route' },
                ],
              })
            }
          >
            标准
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              onChange({
                maxTotalAttempts: 2,
                maxSameTargetAttempts: 2,
                backoff: '300ms',
                maxTotalDuration: '30s',
                rules: [
                  { id: 'retry-503', priority: 10, httpStatuses: [503], action: 'same_credential' },
                  { id: 'retry-429', priority: 20, httpStatuses: [429], action: 'next_credential' },
                ],
              })
            }
          >
            保守
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onChange({ maxTotalAttempts: 1, maxSameTargetAttempts: 1, rules: [] })}
          >
            禁用重试
          </Button>
        </div>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Global retry card                                                            */
/* -------------------------------------------------------------------------- */

function GlobalRetryCard() {
  const qc = useQueryClient();
  const { data: global, isLoading } = useQuery({
    queryKey: ['admin', 'global-policy'],
    queryFn: () => adminConfigApi.getGlobalPolicy(),
  });
  const [draft, setDraft] = useState<RetryPolicy | null>(null);

  useEffect(() => {
    if (global && draft === null) {
      setDraft(global.default_retry ?? { rules: [] });
    }
  }, [global, draft]);

  const save = useMutation({
    mutationFn: (p: RetryPolicy) => adminConfigApi.setGlobalRetry(p),
    onSuccess: () => {
      toast.success('全局重试策略已保存');
      void qc.invalidateQueries({ queryKey: ['admin', 'global-policy'] });
    },
    onError: () => toast.error('保存失败'),
  });

  if (isLoading || !draft) {
    return (
      <Card>
        <CardContent className="p-8 text-center text-sm text-muted-foreground">加载中…</CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Zap className="h-4 w-4" />
          全局重试策略
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-xs text-muted-foreground">
          全局默认策略，所有路由继承。路由可单独配置以覆盖（路由级非空时完全替代全局）。
        </p>
        <RetryPolicyEditor policy={draft} onChange={setDraft} />
        <div className="flex justify-end">
          <Button onClick={() => save.mutate(draft)} disabled={save.isPending}>
            <Save className="h-4 w-4 mr-1" />
            {save.isPending ? '保存中…' : '保存全局策略'}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Route-level retry table                                                      */
/* -------------------------------------------------------------------------- */

function defaultRoutingPolicy(): RoutingPolicy {
  return {
    strategy: 'softmax',
    temperature: 0.7,
    minCandidates: 3,
    weights: { success: 0.45, cost: 0.25, latency: 0.15, quota: 0.1, priority: 0.05 },
  };
}

function RoutingPolicyCard() {
  const qc = useQueryClient();
  const { data: global, isLoading } = useQuery({
    queryKey: ['admin', 'global-policy'],
    queryFn: () => adminConfigApi.getGlobalPolicy(),
  });
  const [draft, setDraft] = useState<RoutingPolicy | null>(null);

  useEffect(() => {
    if (global && draft === null) setDraft(global.routing_policy ?? defaultRoutingPolicy());
  }, [global, draft]);

  const save = useMutation({
    mutationFn: (p: RoutingPolicy) => adminConfigApi.setRoutingPolicy(p),
    onSuccess: () => {
      toast.success('选号策略已保存');
      void qc.invalidateQueries({ queryKey: ['admin', 'global-policy'] });
    },
    onError: () => toast.error('保存失败'),
  });

  if (isLoading || !draft) {
    return <Card><CardContent className="p-8 text-center text-sm text-muted-foreground">加载中…</CardContent></Card>;
  }

  const setWeight = (key: keyof RoutingPolicy['weights'], value: number) => {
    setDraft({ ...draft, weights: { ...draft.weights, [key]: value } });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Zap className="h-4 w-4" />
          全局选号策略
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-xs text-muted-foreground">
          这是账号/路由候选的调度策略配置入口。当前先保存配置，执行侧 softmax 采样和 RPM/TPM 硬过滤会在后续批次接入；候选过少时按 min candidates 走保守策略。
        </p>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="策略">
            <select className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm" value={draft.strategy} onChange={(e) => setDraft({ ...draft, strategy: e.target.value as RoutingPolicy['strategy'] })}>
              <option value="softmax">Softmax 加权采样</option>
              <option value="priority">固定优先级</option>
            </select>
          </Field>
          <Field label="Temperature" hint="0=近似最高分，越大越分散">
            <Input type="number" min={0} max={2} step={0.05} value={draft.temperature} onChange={(e) => setDraft({ ...draft, temperature: Number(e.target.value) })} />
          </Field>
          <Field label="最小候选数" hint="候选少于此值时不过滤">
            <Input type="number" min={1} step={1} value={draft.minCandidates} onChange={(e) => setDraft({ ...draft, minCandidates: Number(e.target.value) })} />
          </Field>
        </div>
        <div className="grid gap-3 sm:grid-cols-5">
          {([
            ['success', '成功率'],
            ['cost', '成本'],
            ['latency', '延迟'],
            ['quota', '额度'],
            ['priority', '优先级'],
          ] as const).map(([key, label]) => (
            <Field key={key} label={label}>
              <Input type="number" min={0} step={0.01} value={draft.weights[key]} onChange={(e) => setWeight(key, Number(e.target.value))} />
            </Field>
          ))}
        </div>
        <div className="flex justify-end">
          <Button onClick={() => save.mutate(draft)} disabled={save.isPending}>
            <Save className="h-4 w-4 mr-1" />
            {save.isPending ? '保存中…' : '保存选号策略'}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function RouteRetryCard() {
  const qc = useQueryClient();
  const { data: routes, isLoading } = useQuery({
    queryKey: ['admin', 'routes'],
    queryFn: () => adminConfigApi.listRoutes(),
  });
  const [editing, setEditing] = useState<AdminRouteConfig | null>(null);
  const [draft, setDraft] = useState<RetryPolicy | null>(null);

  const save = useMutation({
    mutationFn: ({ id, policy }: { id: string; policy: RetryPolicy | null }) =>
      adminConfigApi.updateRoute(id, {
        retryPolicy: policy && (policy.rules?.length || policy.maxTotalAttempts || policy.backoff) ? policy : null,
      }),
    onSuccess: () => {
      toast.success('路由重试策略已保存');
      setEditing(null);
      void qc.invalidateQueries({ queryKey: ['admin', 'routes'] });
    },
    onError: () => toast.error('保存失败'),
  });

  const startEdit = (r: AdminRouteConfig) => {
    setEditing(r);
    setDraft(r.retryPolicy ?? { rules: [] });
  };

  if (isLoading) {
    return (
      <Card>
        <CardContent className="p-8 text-center text-sm text-muted-foreground">加载中…</CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">路由级重试策略</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-xs text-muted-foreground">
          为单个路由配置覆盖全局的策略。留空表示继承全局策略。
        </p>

        {/* Desktop table */}
        <div className="hidden md:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>路由 ID</TableHead>
                <TableHead>模型</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead>当前策略</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(routes ?? []).map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-mono text-xs">{r.id}</TableCell>
                  <TableCell>{r.modelId}</TableCell>
                  <TableCell>{r.providerId}</TableCell>
                  <TableCell>
                    {editing?.id === r.id ? (
                      <span className="text-xs text-warning">编辑中…</span>
                    ) : r.retryPolicy?.rules?.length ? (
                      <div className="flex flex-wrap gap-1">
                        {r.retryPolicy.rules.map((rule) => (
                          <Badge key={rule.id} variant="secondary" className="font-normal">
                            {rule.httpStatuses.join(',')}/{actionLabel(rule.action)}
                          </Badge>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-muted-foreground">继承全局</span>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="outline" onClick={() => startEdit(r)}>
                      编辑
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>

        {/* Mobile card list */}
        <div className="md:hidden space-y-3">
          {(routes ?? []).length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无路由</p>
          ) : (routes ?? []).map((r) => (
            <button
              key={r.id}
              type="button"
              onClick={() => startEdit(r)}
              className="w-full text-left rounded-lg border bg-card p-3 space-y-2 active:bg-accent/50 transition-colors"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-xs truncate">{r.id}</span>
                <span className="text-xs text-muted-foreground shrink-0">{r.modelId}</span>
              </div>
              <p className="text-xs text-muted-foreground">{r.providerId}</p>
              <p className="text-xs">
                {r.retryPolicy?.rules?.length ? (
                  <span className="flex flex-wrap gap-1">
                    {r.retryPolicy.rules.map((rule) => (
                      <Badge key={rule.id} variant="secondary" className="font-normal text-[10px]">
                        {rule.httpStatuses.join(',')}/{actionLabel(rule.action)}
                      </Badge>
                    ))}
                  </span>
                ) : (
                  <span className="text-muted-foreground">继承全局</span>
                )}
              </p>
            </button>
          ))}
        </div>

        {/* Edit dialog (mobile & desktop) */}
        <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
          {editing && draft && (
            <>
              <DialogHeader>
                <DialogTitle className="font-mono text-sm">{editing.id}</DialogTitle>
              </DialogHeader>
              <div className="space-y-4 py-2 max-h-[70vh] overflow-y-auto">
                <div className="text-xs text-muted-foreground">
                  {editing.modelId} · {editing.providerId}
                </div>
                <RetryPolicyEditor policy={draft} onChange={setDraft} />
                <div className="flex gap-2 pt-2 border-t">
                  <Button
                    variant="outline"
                    className="flex-1"
                    onClick={() => setEditing(null)}
                  >
                    取消
                  </Button>
                  <Button
                    className="flex-1"
                    onClick={() => save.mutate({ id: editing.id, policy: draft })}
                    disabled={save.isPending}
                  >
                    {save.isPending ? '保存中…' : '保存'}
                  </Button>
                </div>
              </div>
            </>
          )}
        </Dialog>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Page                                                                         */
/* -------------------------------------------------------------------------- */

export default function RetryPolicyPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="重试策略"
        description="配置上游错误重试行为"
        actions={<PublishStatusHint className="w-full justify-center sm:w-auto" />}
      />

      <RoutingPolicyCard />
      <GlobalRetryCard />
      <RouteRetryCard />

      <Card>
        <CardContent className="flex items-start gap-3 p-4 text-xs text-muted-foreground">
          <ArrowDownToLine className="mt-0.5 h-4 w-4 shrink-0" />
          <div>
            <p className="font-medium text-foreground">重试动作说明</p>
            <ul className="mt-1 space-y-0.5">
              <li><b>同目标重试</b>：同一 route/credential 再试，适合 503 瞬时过载</li>
              <li><b>换密钥</b>：同路由下另一个 credential，适合 429 限流</li>
              <li><b>换路由</b>：同模型另一路由，适合 5xx 上游故障</li>
              <li>路由级策略非空时完全替代全局；留空继承全局</li>
              <li>修改后需到系统设置统一发布，让 executor 热加载新 snapshot</li>
            </ul>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

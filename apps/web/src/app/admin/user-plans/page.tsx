'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminUserPlanApi, adminPlanApi, adminApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Dialog,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { Plus, Ban, RotateCcw, Gift, History } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminLimitOverride, AdminUserPlan, AdminUserPlanInput, LimitOverrideScope } from '@/types/admin';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

const PLAN_TYPE_LABELS: Record<string, string> = {
  coding: '编程',
  token: 'Token',
  image: '图像',
  free: '免费',
};

const STATUS_CONFIG: Record<string, { label: string; variant: 'success' | 'secondary' | 'destructive' }> = {
  active: { label: '有效', variant: 'success' },
  expired: { label: '已过期', variant: 'secondary' },
  cancelled: { label: '已取消', variant: 'destructive' },
};

interface AssignForm {
  userId: string;
  planId: string;
  expiresAt: string;
}

interface OverrideForm {
  userPlan: AdminUserPlan;
  kind: 'reset' | 'bonus';
  scope: LimitOverrideScope;
  bonusRequests: string;
  effectiveUntil: string;
  reason: string;
}

const emptyForm: AssignForm = {
  userId: '',
  planId: '',
  expiresAt: '',
};

const SCOPE_LABELS: Record<LimitOverrideScope, string> = {
  hour5: '5小时滚动',
  weekly: '本周额度',
  period: '本周期总额度',
};

function overrideActive(override: AdminLimitOverride) {
  return !override.effectiveUntil || new Date(override.effectiveUntil).getTime() > Date.now();
}

export default function AdminUserPlansPage() {
  const qc = useQueryClient();

  const { data: userPlans = [], isLoading } = useQuery({
    queryKey: ['admin', 'user-plans'],
    queryFn: adminUserPlanApi.list,
  });

  const { data: plans = [] } = useQuery({
    queryKey: ['admin', 'plans'],
    queryFn: adminPlanApi.list,
  });

  const { data: usersData } = useQuery({
    queryKey: ['admin', 'users', 1, ''],
    queryFn: () => adminApi.listUsers(1, 100, ''),
  });

  const assignMutation = useMutation({
    mutationFn: (input: AdminUserPlanInput) => adminUserPlanApi.assign(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user-plans'] });
      toast.success('套餐已分配');
    },
  });

  const cancelMutation = useMutation({
    mutationFn: (id: string) => adminUserPlanApi.cancel(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user-plans'] });
      toast.success('套餐已撤销');
    },
  });

  const overrideMutation = useMutation({
    mutationFn: (input: OverrideForm) => adminUserPlanApi.createLimitOverride(input.userPlan.id, {
      kind: input.kind,
      scope: input.scope,
      bonusRequests: input.kind === 'bonus' ? Number(input.bonusRequests) : null,
      effectiveUntil: input.effectiveUntil ? new Date(input.effectiveUntil).toISOString() : null,
      reason: input.reason || undefined,
    }),
    onSuccess: (_, variables) => {
      qc.invalidateQueries({ queryKey: ['admin', 'user-plans'] });
      qc.invalidateQueries({ queryKey: ['admin', 'user-plan-overrides', variables.userPlan.id] });
      toast.success('额度调整已生效');
    },
  });

  // Assign dialog
  const [assignOpen, setAssignOpen] = useState(false);
  const [form, setForm] = useState<AssignForm>(emptyForm);

  // Cancel confirm
  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancellingId, setCancellingId] = useState<string | null>(null);

  const [overrideOpen, setOverrideOpen] = useState(false);
  const [overrideForm, setOverrideForm] = useState<OverrideForm | null>(null);
  const [historyPlan, setHistoryPlan] = useState<AdminUserPlan | null>(null);

  const { data: overrideHistory = [], isFetching: loadingHistory } = useQuery({
    queryKey: ['admin', 'user-plan-overrides', historyPlan?.id],
    queryFn: () => adminUserPlanApi.listLimitOverrides(historyPlan?.id ?? ''),
    enabled: Boolean(historyPlan?.id),
  });

  const revokeOverrideMutation = useMutation({
    mutationFn: (id: string) => adminUserPlanApi.revokeLimitOverride(id),
    onSuccess: () => {
      if (historyPlan?.id) {
        qc.invalidateQueries({ queryKey: ['admin', 'user-plan-overrides', historyPlan.id] });
      }
      qc.invalidateQueries({ queryKey: ['admin', 'user-plans'] });
      toast.success('覆盖已撤销');
    },
  });

  function openAssign() {
    setForm(emptyForm);
    setAssignOpen(true);
  }

  function handleAssign() {
    if (!form.userId) {
      toast.error('请选择用户');
      return;
    }
    if (!form.planId) {
      toast.error('请选择套餐');
      return;
    }
    const input: AdminUserPlanInput = {
      userId: form.userId,
      planId: form.planId,
      expiresAt: form.expiresAt ? new Date(form.expiresAt).toISOString() : null,
    };
    assignMutation.mutate(input, { onSuccess: () => setAssignOpen(false) });
  }

  function openCancel(id: string) {
    setCancellingId(id);
    setCancelOpen(true);
  }

  function handleCancel() {
    if (!cancellingId) return;
    cancelMutation.mutate(cancellingId, { onSuccess: () => setCancelOpen(false) });
  }

  function openOverride(userPlan: AdminUserPlan, kind: 'reset' | 'bonus') {
    setOverrideForm({
      userPlan,
      kind,
      scope: 'hour5',
      bonusRequests: kind === 'bonus' ? '100' : '',
      effectiveUntil: '',
      reason: '',
    });
    setOverrideOpen(true);
  }

  function handleOverride() {
    if (!overrideForm) return;
    if (overrideForm.kind === 'bonus' && (!overrideForm.bonusRequests || Number(overrideForm.bonusRequests) <= 0)) {
      toast.error('请输入大于 0 的加额次数');
      return;
    }
    overrideMutation.mutate(overrideForm, { onSuccess: () => setOverrideOpen(false) });
  }

  function openHistory(userPlan: AdminUserPlan) {
    setHistoryPlan(userPlan);
  }

  function handleRevokeOverride(id: string) {
    revokeOverrideMutation.mutate(id);
  }

  const users = usersData?.users ?? [];

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      {/* Top bar */}
      <div className="flex justify-end">
        <Button onClick={openAssign}>
          <Plus />
          分配套餐
        </Button>
      </div>

      {userPlans.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无用户套餐</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>用户</TableHead>
                  <TableHead>套餐</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>激活时间</TableHead>
                  <TableHead>到期</TableHead>
                  <TableHead>剩余额度</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {userPlans.map((up) => {
                  const statusCfg = STATUS_CONFIG[up.status] ?? { label: up.status, variant: 'secondary' as const };
                  return (
                    <TableRow key={up.id}>
                      <TableCell className="font-medium">{up.userEmail || up.userId}</TableCell>
                      <TableCell>{up.planName || `#${up.planId}`}</TableCell>
                      <TableCell>
                        <Badge variant="outline">
                          {PLAN_TYPE_LABELS[up.planType] ?? up.planType}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusCfg.variant}>{statusCfg.label}</Badge>
                      </TableCell>
                      <TableCell>{formatTime(up.activatedAt)}</TableCell>
                      <TableCell>{up.expiresAt ? formatTime(up.expiresAt) : '永久'}</TableCell>
                      <TableCell>{Number(up.remainingQuota).toLocaleString()}</TableCell>
                      <TableCell className="text-right">
                        {up.status === 'active' && (
                          <div className="flex justify-end gap-1">
                            {up.planType === 'coding' && (
                              <>
                                <Button variant="ghost" size="icon" title="重置窗口" onClick={() => openOverride(up, 'reset')}>
                                  <RotateCcw className="h-4 w-4" />
                                </Button>
                                <Button variant="ghost" size="icon" title="临时加额" onClick={() => openOverride(up, 'bonus')}>
                                  <Gift className="h-4 w-4" />
                                </Button>
                                <Button variant="ghost" size="icon" title="覆盖历史" onClick={() => openHistory(up)}>
                                  <History className="h-4 w-4" />
                                </Button>
                              </>
                            )}
                            <Button
                              variant="ghost"
                              size="icon"
                              className="text-destructive"
                              onClick={() => openCancel(up.id)}
                            >
                              <Ban className="h-4 w-4" />
                            </Button>
                          </div>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {userPlans.map((up) => {
              const statusCfg = STATUS_CONFIG[up.status] ?? { label: up.status, variant: 'secondary' as const };
              return (
                <Card key={up.id}>
                  <CardContent className="p-4 space-y-3">
                    <div className="flex items-start justify-between gap-2">
                      <div>
                        <div className="font-medium">{up.userEmail || up.userId}</div>
                        <div className="text-sm text-muted-foreground">{up.planName || `#${up.planId}`}</div>
                      </div>
                      <div className="flex items-center gap-1.5 shrink-0">
                        <Badge variant="outline">
                          {PLAN_TYPE_LABELS[up.planType] ?? up.planType}
                        </Badge>
                        <Badge variant={statusCfg.variant}>{statusCfg.label}</Badge>
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                      <div className="text-muted-foreground">到期</div>
                      <div>{up.expiresAt ? formatTime(up.expiresAt) : '永久'}</div>
                      <div className="text-muted-foreground">剩余额度</div>
                      <div>{Number(up.remainingQuota).toLocaleString()}</div>
                    </div>
                    {up.status === 'active' && (
                      <div className="flex flex-wrap justify-end gap-2">
                        {up.planType === 'coding' && (
                          <>
                            <Button variant="outline" size="sm" onClick={() => openOverride(up, 'reset')}>
                              <RotateCcw className="h-4 w-4" />
                              重置
                            </Button>
                            <Button variant="outline" size="sm" onClick={() => openOverride(up, 'bonus')}>
                              <Gift className="h-4 w-4" />
                              加额
                            </Button>
                            <Button variant="outline" size="sm" onClick={() => openHistory(up)}>
                              <History className="h-4 w-4" />
                              历史
                            </Button>
                          </>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive"
                          onClick={() => openCancel(up.id)}
                        >
                          <Ban className="h-4 w-4" />
                          撤销
                        </Button>
                      </div>
                    )}
                  </CardContent>
                </Card>
              );
            })}
          </div>
        </>
      )}

      {/* Assign Dialog */}
      <Dialog open={assignOpen} onOpenChange={setAssignOpen}>
        <DialogHeader>
          <DialogTitle>分配套餐</DialogTitle>
          <DialogDescription>为用户分配套餐，到期时间留空表示永久有效。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="up-user">用户</Label>
            <select
              id="up-user"
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              value={form.userId}
              onChange={(e) => setForm((f) => ({ ...f, userId: e.target.value }))}
            >
              <option value="">选择用户</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.email}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="up-plan">套餐</Label>
            <select
              id="up-plan"
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              value={form.planId}
              onChange={(e) => setForm((f) => ({ ...f, planId: e.target.value }))}
            >
              <option value="">选择套餐</option>
              {plans.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="up-expires">到期时间（选填）</Label>
            <Input
              id="up-expires"
              type="datetime-local"
              value={form.expiresAt}
              onChange={(e) => setForm((f) => ({ ...f, expiresAt: e.target.value }))}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setAssignOpen(false)} disabled={assignMutation.isPending}>
            取消
          </Button>
          <Button onClick={handleAssign} disabled={assignMutation.isPending}>
            {assignMutation.isPending ? '分配中…' : '分配'}
          </Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={overrideOpen} onOpenChange={setOverrideOpen}>
        <DialogHeader>
          <DialogTitle>{overrideForm?.kind === 'bonus' ? '临时加额' : '重置用量窗口'}</DialogTitle>
          <DialogDescription>
            {overrideForm?.kind === 'bonus'
              ? '为该用户套餐在指定窗口临时增加可用请求次数，不修改历史请求记录。'
              : '从现在开始重置指定窗口的计数起点，不修改历史请求记录。'}
          </DialogDescription>
        </DialogHeader>
        {overrideForm && (
          <div className="space-y-4">
            <div className="rounded-md bg-muted p-3 text-sm">
              <div className="font-medium">{overrideForm.userPlan.userEmail || overrideForm.userPlan.userId}</div>
              <div className="text-muted-foreground">{overrideForm.userPlan.planName}</div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="override-scope">窗口</Label>
              <select
                id="override-scope"
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                value={overrideForm.scope}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, scope: e.target.value as LimitOverrideScope } : f)}
              >
                {Object.entries(SCOPE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </select>
            </div>
            {overrideForm.kind === 'bonus' && (
              <div className="space-y-2">
                <Label htmlFor="override-bonus">加额次数</Label>
                <Input
                  id="override-bonus"
                  type="number"
                  min={1}
                  value={overrideForm.bonusRequests}
                  onChange={(e) => setOverrideForm((f) => f ? { ...f, bonusRequests: e.target.value } : f)}
                />
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="override-until">有效期至（选填）</Label>
              <Input
                id="override-until"
                type="datetime-local"
                value={overrideForm.effectiveUntil}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, effectiveUntil: e.target.value } : f)}
              />
              <p className="text-xs text-muted-foreground">留空表示长期有效；重置窗口通常可留空。</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="override-reason">原因（选填）</Label>
              <Input
                id="override-reason"
                value={overrideForm.reason}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, reason: e.target.value } : f)}
                placeholder="例如：客服补偿 / 活动加赠"
              />
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setOverrideOpen(false)} disabled={overrideMutation.isPending}>取消</Button>
          <Button onClick={handleOverride} disabled={overrideMutation.isPending}>
            {overrideMutation.isPending ? '提交中…' : '确认'}
          </Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={Boolean(historyPlan)} onOpenChange={(open) => { if (!open) setHistoryPlan(null); }}>
        <DialogHeader>
          <DialogTitle>额度覆盖历史</DialogTitle>
          <DialogDescription>
            查看和撤销该用户套餐的重置/加额记录。撤销不会删除记录，只会让它从现在起失效。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {historyPlan && (
            <div className="rounded-md bg-muted p-3 text-sm">
              <div className="font-medium">{historyPlan.userEmail || historyPlan.userId}</div>
              <div className="text-muted-foreground">{historyPlan.planName}</div>
            </div>
          )}
          {loadingHistory ? (
            <div className="py-8 text-center text-sm text-muted-foreground">加载中…</div>
          ) : overrideHistory.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">暂无覆盖记录</div>
          ) : (
            <div className="max-h-[420px] overflow-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>类型</TableHead>
                    <TableHead>窗口</TableHead>
                    <TableHead>加额</TableHead>
                    <TableHead>生效时间</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>原因</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {overrideHistory.map((ov) => {
                    const active = overrideActive(ov);
                    return (
                      <TableRow key={ov.id}>
                        <TableCell>
                          <Badge variant={ov.kind === 'bonus' ? 'success' : 'outline'}>
                            {ov.kind === 'bonus' ? '加额' : '重置'}
                          </Badge>
                        </TableCell>
                        <TableCell>{SCOPE_LABELS[ov.scope]}</TableCell>
                        <TableCell>{ov.kind === 'bonus' ? Number(ov.bonusRequests ?? 0).toLocaleString() : '—'}</TableCell>
                        <TableCell className="whitespace-nowrap">{formatTime(ov.effectiveFrom)}</TableCell>
                        <TableCell>
                          <Badge variant={active ? 'success' : 'secondary'}>{active ? '生效中' : '已失效'}</Badge>
                          {ov.effectiveUntil && (
                            <div className="mt-1 whitespace-nowrap text-xs text-muted-foreground">至 {formatTime(ov.effectiveUntil)}</div>
                          )}
                        </TableCell>
                        <TableCell className="max-w-48 truncate" title={ov.reason || undefined}>{ov.reason || '—'}</TableCell>
                        <TableCell className="text-right">
                          {active ? (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-destructive"
                              disabled={revokeOverrideMutation.isPending}
                              onClick={() => handleRevokeOverride(ov.id)}
                            >
                              撤销
                            </Button>
                          ) : '—'}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setHistoryPlan(null)}>关闭</Button>
        </DialogFooter>
      </Dialog>

      {/* Cancel Confirm */}
      <ConfirmDialog
        open={cancelOpen}
        onOpenChange={setCancelOpen}
        title="撤销套餐"
        description="确定要撤销此用户套餐吗？撤销后用户将失去该套餐权益。"
        confirmText="撤销"
        destructive
        loading={cancelMutation.isPending}
        onConfirm={handleCancel}
      />
    </div>
  );
}

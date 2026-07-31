'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi, adminPlanApi, adminUserPlanApi } from '@/lib/api/admin';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
import { ArrowLeft, Plus } from 'lucide-react';
import { toast } from 'sonner';
import type { ApiKey, UserPlan, RequestLog } from '@/types';
import type { AdminLimitOverride, AdminUserPlanInput, LimitOverrideScope } from '@/types/admin';
import { PageHeader } from '@/components/page-header';

function formatTime(iso: string | null) {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('zh-CN');
}

function formatDate(iso: string | null) {
  if (!iso) return '—';
  return new Date(iso).toLocaleDateString('zh-CN');
}

function formatPlanLimit(plan: UserPlan) {
  if (plan.planType === 'token') return `${Number(plan.totalQuota || 0).toLocaleString()} tokens`;
  return `${Number(plan.totalQuota || 0).toLocaleString()} 次`;
}

function apiKeyStatusVariant(status: string) {
  return status === 'active' ? ('success' as const) : ('destructive' as const);
}

function userPlanStatusVariant(status: string) {
  if (status === 'active') return 'success' as const;
  if (status === 'expired') return 'warning' as const;
  return 'destructive' as const;
}

function userPlanStatusLabel(status: string) {
  if (status === 'active') return '生效中';
  if (status === 'expired') return '已过期';
  if (status === 'cancelled') return '已撤销';
  return '已禁用';
}

function requestStatusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

interface AssignForm {
  planId: string;
  expiresAt: string;
}

interface OverrideForm {
  userPlan: UserPlan;
  kind: 'reset' | 'bonus';
  scope: LimitOverrideScope;
  bonusRequests: string;
  effectiveUntil: string;
  reason: string;
}

interface RenewSwitchForm {
  userPlan: UserPlan;
  mode: 'renew' | 'switch';
  extendDays: string;
  newPlanId: string;
  expiresAt: string;
}

const SCOPE_LABELS: Record<LimitOverrideScope, string> = {
  hour5: '5小时滚动',
  weekly: '本周额度',
  period: '本周期总额度',
};

function overrideActive(override: AdminLimitOverride) {
  return !override.effectiveUntil || new Date(override.effectiveUntil).getTime() > Date.now();
}

function toDateTimeLocalValue(date: Date) {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function addDaysLocal(days: number) {
  const d = new Date();
  d.setDate(d.getDate() + days);
  return toDateTimeLocalValue(d);
}

function defaultPeriodDays(category?: string | null) {
  switch (category) {
    case 'daily': return 1;
    case 'weekly': return 7;
    case 'quarterly': return 90;
    case 'yearly': return 365;
    case 'monthly':
    default:
      return 30;
  }
}

export default function AdminUserDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const qc = useQueryClient();

  const { data: user, isLoading } = useQuery({
    queryKey: ['admin', 'user', id],
    queryFn: () => adminApi.getUser(id),
    enabled: !!id,
  });

  const { data: plans = [] } = useQuery({
    queryKey: ['admin', 'plans'],
    queryFn: adminPlanApi.list,
  });

  const [assignOpen, setAssignOpen] = useState(false);
  const [assignForm, setAssignForm] = useState<AssignForm>({ planId: '', expiresAt: '' });
  const [cancelPlanId, setCancelPlanId] = useState<string | null>(null);
  const [renewSwitchOpen, setRenewSwitchOpen] = useState(false);
  const [renewSwitchForm, setRenewSwitchForm] = useState<RenewSwitchForm | null>(null);
  const [overrideOpen, setOverrideOpen] = useState(false);
  const [overrideForm, setOverrideForm] = useState<OverrideForm | null>(null);
  const [historyPlan, setHistoryPlan] = useState<UserPlan | null>(null);

  const { data: overrideHistory = [], isFetching: loadingHistory } = useQuery({
    queryKey: ['admin', 'user-plan-overrides', historyPlan?.id],
    queryFn: () => adminUserPlanApi.listLimitOverrides(historyPlan?.id ?? ''),
    enabled: Boolean(historyPlan?.id),
  });

  const assignMutation = useMutation({
    mutationFn: (input: AdminUserPlanInput) => adminUserPlanApi.assign(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user', id] });
      toast.success('套餐已分配');
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : '套餐分配失败');
    },
  });

  const cancelMutation = useMutation({
    mutationFn: (planID: string) => adminUserPlanApi.cancel(planID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user', id] });
      toast.success('套餐已撤销');
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : '套餐撤销失败');
    },
  });

  const renewSwitchMutation = useMutation({
    mutationFn: (input: RenewSwitchForm) => {
      if (input.mode === 'switch') {
        return adminUserPlanApi.switchPlan(input.userPlan.id, {
          planId: input.newPlanId,
          expiresAt: input.expiresAt ? new Date(input.expiresAt).toISOString() : null,
        });
      }
      return adminUserPlanApi.renew(input.userPlan.id, {
        extendDays: input.extendDays ? Number(input.extendDays) : null,
        expiresAt: input.expiresAt ? new Date(input.expiresAt).toISOString() : null,
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user', id] });
      toast.success('套餐已更新');
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : '套餐更新失败');
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
      qc.invalidateQueries({ queryKey: ['admin', 'user', id] });
      qc.invalidateQueries({ queryKey: ['admin', 'user-plan-overrides', variables.userPlan.id] });
      toast.success('额度调整已生效');
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : '额度调整失败');
    },
  });

  const revokeOverrideMutation = useMutation({
    mutationFn: (overrideID: string) => adminUserPlanApi.revokeLimitOverride(overrideID),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'user', id] });
      if (historyPlan?.id) qc.invalidateQueries({ queryKey: ['admin', 'user-plan-overrides', historyPlan.id] });
      toast.success('覆盖已撤销');
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : '撤销覆盖失败');
    },
  });

  function handleAssignPlanChange(planId: string) {
    const plan = plans.find((p) => p.id === planId);
    setAssignForm((f) => ({
      ...f,
      planId,
      expiresAt: plan ? addDaysLocal(defaultPeriodDays(plan.category)) : f.expiresAt,
    }));
  }

  function handleAssign() {
    if (!id) return;
    if (!assignForm.planId) {
      toast.error('请选择套餐');
      return;
    }
    assignMutation.mutate({
      userId: id,
      planId: assignForm.planId,
      expiresAt: assignForm.expiresAt ? new Date(assignForm.expiresAt).toISOString() : null,
    }, { onSuccess: () => setAssignOpen(false) });
  }

  function openRenewSwitch(userPlan: UserPlan, mode: 'renew' | 'switch') {
    setRenewSwitchForm({
      userPlan,
      mode,
      extendDays: mode === 'renew' ? String(defaultPeriodDays(userPlan.category)) : '',
      newPlanId: mode === 'switch' ? userPlan.planId : '',
      expiresAt: '',
    });
    setRenewSwitchOpen(true);
  }

  function handleRenewSwitch() {
    if (!renewSwitchForm) return;
    if (renewSwitchForm.mode === 'renew' && !renewSwitchForm.extendDays && !renewSwitchForm.expiresAt) {
      toast.error('请输入延长天数或新的到期时间');
      return;
    }
    if (renewSwitchForm.mode === 'switch' && !renewSwitchForm.newPlanId) {
      toast.error('请选择切换后的套餐');
      return;
    }
    renewSwitchMutation.mutate(renewSwitchForm, { onSuccess: () => setRenewSwitchOpen(false) });
  }

  function openOverride(userPlan: UserPlan, kind: 'reset' | 'bonus') {
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

  const switchCandidates = renewSwitchForm?.mode === 'switch'
    ? plans.filter((p) => p.status === 'active' && p.planType === renewSwitchForm.userPlan.planType)
    : [];

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
      </div>
    );
  }

  if (!user) {
    return (
      <div className="space-y-4">
        <Link href="/admin/users" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" />
          返回用户列表
        </Link>
        <p className="text-center text-muted-foreground py-20">用户不存在</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader title="用户详情" actions={
        <Link href="/admin/users" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" />
          返回用户列表
        </Link>
      } />

      {/* User info card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">用户信息</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-sm">
            <div>
              <span className="text-muted-foreground">邮箱</span>
              <p className="font-medium mt-0.5">{user.email}</p>
            </div>
            <div>
              <span className="text-muted-foreground">角色</span>
              <p className="mt-0.5">
                <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                  {user.role === 'admin' ? '管理员' : '用户'}
                </Badge>
              </p>
            </div>
            <div>
              <span className="text-muted-foreground">状态</span>
              <p className="mt-0.5">
                <Badge variant={user.status === 'active' ? 'success' : 'destructive'}>
                  {user.status === 'active' ? '正常' : '已禁用'}
                </Badge>
              </p>
            </div>
            <div>
              <span className="text-muted-foreground">注册时间</span>
              <p className="font-medium mt-0.5">{formatTime(user.createdAt)}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* User Plans card */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <CardTitle className="text-base">套餐</CardTitle>
          <Button size="sm" onClick={() => {
            setAssignForm({ planId: '', expiresAt: '' });
            setAssignOpen(true);
          }}>
            <Plus className="h-4 w-4" />
            分配套餐
          </Button>
        </CardHeader>
        <CardContent>
          {user.userPlans.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无套餐</p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card">
            <Table>
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead>套餐</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>总额度</TableHead>
                  <TableHead>剩余额度</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>到期</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.userPlans.map((plan: UserPlan) => (
                  <TableRow key={plan.id}>
                    <TableCell className="font-medium">{plan.planName || `#${plan.planId}`}</TableCell>
                    <TableCell >{plan.planType === 'token' ? 'Token' : '编程'}</TableCell>
                    <TableCell >{formatPlanLimit(plan)}</TableCell>
                    <TableCell >{Number(plan.remainingQuota || 0).toLocaleString()}</TableCell>
                    <TableCell>
                      <Badge variant={userPlanStatusVariant(plan.status)}>
                        {userPlanStatusLabel(plan.status)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatDate(plan.expiresAt)}
                    </TableCell>
                    <TableCell className="text-right">
                      {plan.status === 'active' && (
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button variant="outline" size="sm" onClick={() => openRenewSwitch(plan, 'renew')}>续费</Button>
                          <Button variant="outline" size="sm" onClick={() => openRenewSwitch(plan, 'switch')}>切换</Button>
                          {plan.planType === 'coding' && (
                            <>
                              <Button variant="outline" size="sm" onClick={() => openOverride(plan, 'reset')}>重置</Button>
                              <Button variant="outline" size="sm" onClick={() => openOverride(plan, 'bonus')}>加额</Button>
                              <Button variant="ghost" size="sm" onClick={() => setHistoryPlan(plan)}>历史</Button>
                            </>
                          )}
                          <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setCancelPlanId(plan.id)}>撤销</Button>
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          )}
        </CardContent>
      </Card>

      {/* API Keys card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">API 密钥（最近 5 条）</CardTitle>
        </CardHeader>
        <CardContent>
          {user.apiKeys.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无 API 密钥</p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card">
            <Table>
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>密钥</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>创建时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.apiKeys.slice(0, 5).map((key: ApiKey) => (
                  <TableRow key={key.id}>
                    <TableCell >{key.name}</TableCell>
                    <TableCell className="font-mono">
                      {key.keyPrefix}…{key.keySuffix}
                    </TableCell>
                    <TableCell>
                      <Badge variant={apiKeyStatusVariant(key.status)}>
                        {key.status === 'active' ? '正常' : '已禁用'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatTime(key.createdAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          )}
        </CardContent>
      </Card>

      {/* Recent Requests card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">最近请求（5 条）</CardTitle>
        </CardHeader>
        <CardContent>
          {user.recentRequests.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无请求记录</p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card">
            <Table>
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead>模型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>耗时</TableHead>
                  <TableHead>时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.recentRequests.slice(0, 5).map((req: RequestLog) => (
                  <TableRow key={req.requestId}>
                    <TableCell >{req.model}</TableCell>
                    <TableCell>
                      <Badge variant={requestStatusVariant(req.status)}>
                        {req.status === 'success' ? '成功' : '失败'}
                      </Badge>
                    </TableCell>
                    <TableCell >
                      {req.durationMs != null ? `${req.durationMs}ms` : '—'}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatTime(req.createdAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          )}
        </CardContent>
      </Card>

      {/* Stats row */}
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        总请求数：<span className="font-medium text-foreground">{user.totalRequests.toLocaleString()}</span>
      </div>

      <Dialog open={assignOpen} onOpenChange={setAssignOpen}>
        <DialogHeader>
          <DialogTitle>分配套餐</DialogTitle>
          <DialogDescription>为 {user.email} 分配套餐，到期时间留空表示永久有效。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="detail-plan">套餐</Label>
            <select
              id="detail-plan"
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              value={assignForm.planId}
              onChange={(e) => handleAssignPlanChange(e.target.value)}
            >
              <option value="">选择套餐</option>
              {plans.map((p) => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="detail-expires">到期时间（选填）</Label>
            <Input
              id="detail-expires"
              type="datetime-local"
              value={assignForm.expiresAt}
              onChange={(e) => setAssignForm((f) => ({ ...f, expiresAt: e.target.value }))}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setAssignOpen(false)} disabled={assignMutation.isPending}>取消</Button>
          <Button onClick={handleAssign} disabled={assignMutation.isPending}>{assignMutation.isPending ? '分配中…' : '分配'}</Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={renewSwitchOpen} onOpenChange={setRenewSwitchOpen}>
        <DialogHeader>
          <DialogTitle>{renewSwitchForm?.mode === 'switch' ? '切换套餐' : '续费套餐'}</DialogTitle>
          <DialogDescription>
            {renewSwitchForm?.mode === 'switch'
              ? '切换会取消当前套餐并创建新的套餐绑定；默认保留原到期时间，只有填写新到期时间才会覆盖。历史请求和账本不修改。'
              : '按套餐周期延长当前到期时间，或直接设置新的到期时间。'}
          </DialogDescription>
        </DialogHeader>
        {renewSwitchForm && (
          <div className="space-y-4">
            <div className="rounded-md bg-muted p-3 text-sm">
              <div className="font-medium">{renewSwitchForm.userPlan.planName || `#${renewSwitchForm.userPlan.planId}`}</div>
              <div className="text-muted-foreground">当前到期：{formatTime(renewSwitchForm.userPlan.expiresAt)}</div>
            </div>
            {renewSwitchForm.mode === 'renew' ? (
              <div className="space-y-2">
                <Label htmlFor="renew-days">延长天数</Label>
                <Input
                  id="renew-days"
                  type="number"
                  min={1}
                  value={renewSwitchForm.extendDays}
                  onChange={(e) => setRenewSwitchForm((f) => f ? { ...f, extendDays: e.target.value } : f)}
                />
                <p className="text-muted-foreground">默认按套餐周期填充：天卡 1 天、周卡 7 天、月卡 30 天、季卡 90 天、年卡 365 天；也可以清空天数，改用下面的新到期时间。</p>
              </div>
            ) : (
              <div className="space-y-2">
                <Label htmlFor="switch-plan">切换后的套餐</Label>
                <select
                  id="switch-plan"
                  className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                  value={renewSwitchForm.newPlanId}
                  onChange={(e) => setRenewSwitchForm((f) => f ? { ...f, newPlanId: e.target.value } : f)}
                >
                  <option value="">选择套餐</option>
                  {switchCandidates.map((p) => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
                <p className="text-muted-foreground">只显示同类型候选；后端会校验目标套餐用量不能低于当前套餐。</p>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="renew-expires">新的到期时间（选填）</Label>
              <Input
                id="renew-expires"
                type="datetime-local"
                value={renewSwitchForm.expiresAt}
                onChange={(e) => setRenewSwitchForm((f) => f ? { ...f, expiresAt: e.target.value } : f)}
              />
              <p className="text-muted-foreground">填写后会优先使用此到期时间；切换时留空会保留原到期时间。</p>
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setRenewSwitchOpen(false)} disabled={renewSwitchMutation.isPending}>取消</Button>
          <Button onClick={handleRenewSwitch} disabled={renewSwitchMutation.isPending}>{renewSwitchMutation.isPending ? '提交中…' : '确认'}</Button>
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
              <div className="font-medium">{overrideForm.userPlan.planName || `#${overrideForm.userPlan.planId}`}</div>
              <div className="text-muted-foreground">{overrideForm.userPlan.planType === 'token' ? 'Token' : '编程'}套餐</div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="detail-override-scope">窗口</Label>
              <select
                id="detail-override-scope"
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                value={overrideForm.scope}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, scope: e.target.value as LimitOverrideScope } : f)}
              >
                {Object.entries(SCOPE_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
            </div>
            {overrideForm.kind === 'bonus' && (
              <div className="space-y-2">
                <Label htmlFor="detail-override-bonus">加额次数</Label>
                <Input
                  id="detail-override-bonus"
                  type="number"
                  min={1}
                  value={overrideForm.bonusRequests}
                  onChange={(e) => setOverrideForm((f) => f ? { ...f, bonusRequests: e.target.value } : f)}
                />
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="detail-override-until">有效期至（选填）</Label>
              <Input
                id="detail-override-until"
                type="datetime-local"
                value={overrideForm.effectiveUntil}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, effectiveUntil: e.target.value } : f)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="detail-override-reason">原因（选填）</Label>
              <Input
                id="detail-override-reason"
                value={overrideForm.reason}
                onChange={(e) => setOverrideForm((f) => f ? { ...f, reason: e.target.value } : f)}
                placeholder="例如：客服补偿 / 活动加赠"
              />
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setOverrideOpen(false)} disabled={overrideMutation.isPending}>取消</Button>
          <Button onClick={handleOverride} disabled={overrideMutation.isPending}>{overrideMutation.isPending ? '提交中…' : '确认'}</Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={Boolean(historyPlan)} onOpenChange={(open) => { if (!open) setHistoryPlan(null); }}>
        <DialogHeader>
          <DialogTitle>额度覆盖历史</DialogTitle>
          <DialogDescription>查看和撤销该用户套餐的重置/加额记录。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {historyPlan && (
            <div className="rounded-md bg-muted p-3 text-sm">
              <div className="font-medium">{historyPlan.planName || `#${historyPlan.planId}`}</div>
              <div className="text-muted-foreground">{user.email}</div>
            </div>
          )}
          {loadingHistory ? (
            <div className="py-8 text-center text-sm text-muted-foreground">加载中…</div>
          ) : overrideHistory.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">暂无覆盖记录</div>
          ) : (
            <div className="max-h-[420px] overflow-hidden rounded-lg border border-border bg-card">
              <Table>
                <TableHeader className="bg-muted/30">
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
                        <TableCell><Badge variant={ov.kind === 'bonus' ? 'success' : 'outline'}>{ov.kind === 'bonus' ? '加额' : '重置'}</Badge></TableCell>
                        <TableCell>{SCOPE_LABELS[ov.scope]}</TableCell>
                        <TableCell>{ov.kind === 'bonus' ? Number(ov.bonusRequests ?? 0).toLocaleString() : '—'}</TableCell>
                        <TableCell className="whitespace-nowrap">{formatTime(ov.effectiveFrom)}</TableCell>
                        <TableCell>
                          <Badge variant={active ? 'success' : 'secondary'}>{active ? '生效中' : '已失效'}</Badge>
                          {ov.effectiveUntil && <div className="mt-1 whitespace-nowrap text-xs text-muted-foreground">至 {formatTime(ov.effectiveUntil)}</div>}
                        </TableCell>
                        <TableCell className="max-w-48 truncate" title={ov.reason || undefined}>{ov.reason || '—'}</TableCell>
                        <TableCell className="text-right">
                          {active ? (
                            <Button variant="ghost" size="sm" className="text-destructive" disabled={revokeOverrideMutation.isPending} onClick={() => revokeOverrideMutation.mutate(ov.id)}>
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

      <ConfirmDialog
        open={Boolean(cancelPlanId)}
        onOpenChange={(open) => { if (!open) setCancelPlanId(null); }}
        title="撤销套餐"
        description="确定要撤销此用户套餐吗？撤销后用户将失去该套餐权益。"
        confirmText="撤销"
        destructive
        loading={cancelMutation.isPending}
        onConfirm={() => {
          if (cancelPlanId) cancelMutation.mutate(cancelPlanId, { onSuccess: () => setCancelPlanId(null) });
        }}
      />
    </div>
  );
}

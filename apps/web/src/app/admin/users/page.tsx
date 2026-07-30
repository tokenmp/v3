'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, Package, Plus } from 'lucide-react';
import { toast } from 'sonner';
import { adminApi, adminPlanApi, adminUserPlanApi } from '@/lib/api/admin';
import { PageHeader } from '@/components/page-header';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { FilterChip } from '@/components/filter-chip';
import { Badge } from '@/components/ui/badge';
import { BottomSheet } from '@/components/ui/bottom-sheet';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { inputCls } from '@/components/ui/field';
import { Modal } from '@/components/ui/modal';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { AdminUser, AdminUserDetail, AdminUserPlan } from '@/types/admin';

function formatTime(iso: string | null) {
  return iso ? new Date(iso).toLocaleString('zh-CN') : '—';
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

const API_KEY_STATUS_CONFIG = {
  active: { label: '正常', variant: 'success' as const },
  disabled: { label: '已禁用', variant: 'destructive' as const },
  revoked: { label: '已撤销', variant: 'secondary' as const },
};

const REQUEST_STATUS_CONFIG = {
  success: { label: '成功', variant: 'success' as const },
  error: { label: '失败', variant: 'destructive' as const },
  processing: { label: '处理中', variant: 'warning' as const },
  cancelled: { label: '已取消', variant: 'secondary' as const },
};

function UserDetailBody({
  user,
  detail,
  updating,
  onAssignPlan,
  onCancelPlan,
  onToggleStatus,
  onToggleRole,
}: {
  user: AdminUser;
  detail: AdminUserDetail | undefined;
  updating: boolean;
  onAssignPlan: () => void;
  onCancelPlan: (plan: AdminUserPlan) => void;
  onToggleStatus: (user: AdminUser) => void;
  onToggleRole: (user: AdminUser) => void;
}) {
  const displayedUser = detail ?? user;
  const userPlans = (detail?.userPlans ?? []) as unknown as AdminUserPlan[];
  const activePlans = userPlans.filter((plan) => plan.status === 'active');
  const otherPlans = userPlans.filter((plan) => plan.status !== 'active');

  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <h3 className="text-sm font-medium">资料</h3>
        <dl className="grid gap-3 rounded-lg border border-border bg-muted/20 p-4 sm:grid-cols-3">
          <div className="space-y-1">
            <dt className="text-xs text-muted-foreground">角色</dt>
            <dd>
              <Badge variant={displayedUser.role === 'admin' ? 'default' : 'secondary'}>
                {displayedUser.role === 'admin' ? '管理员' : '普通用户'}
              </Badge>
            </dd>
          </div>
          <div className="space-y-1">
            <dt className="text-xs text-muted-foreground">状态</dt>
            <dd>
              <Badge variant={displayedUser.status === 'active' ? 'success' : 'destructive'}>
                {displayedUser.status === 'active' ? '正常' : '已禁用'}
              </Badge>
            </dd>
          </div>
          <div className="space-y-1">
            <dt className="text-xs text-muted-foreground">注册时间</dt>
            <dd className="text-sm">{formatTime(displayedUser.createdAt)}</dd>
          </div>
        </dl>
      </section>

      <section className="space-y-3 border-t border-border pt-5">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <Package className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">套餐</h3>
          </div>
          <Button variant="outline" size="sm" onClick={onAssignPlan}>
            <Plus className="size-3.5" />
            分配套餐
          </Button>
        </div>

        {!detail ? (
          <p className="rounded-lg border border-dashed border-border py-6 text-center text-sm text-muted-foreground">正在加载详情…</p>
        ) : (
          <>
            <div className="space-y-2">
              <p className="text-xs font-medium text-muted-foreground">当前套餐</p>
              {activePlans.length === 0 ? (
                <p className="rounded-lg border border-dashed border-border py-5 text-center text-sm text-muted-foreground">暂无有效套餐</p>
              ) : (
                <div className="grid gap-3 lg:grid-cols-2">
                  {activePlans.map((plan) => (
                    <div key={plan.id} className="space-y-3 rounded-lg border border-border p-4">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium">{plan.planName}</p>
                          <p className="mt-0.5 text-xs text-muted-foreground">
                            {PLAN_TYPE_LABELS[plan.planType] ?? plan.planType}
                          </p>
                        </div>
                        <Badge variant="success">有效</Badge>
                      </div>
                      <dl className="grid grid-cols-2 gap-3 text-xs">
                        <div>
                          <dt className="text-muted-foreground">剩余额度</dt>
                          <dd className="mt-1 font-mono text-foreground">{Number(plan.remainingQuota).toLocaleString()}</dd>
                        </div>
                        <div>
                          <dt className="text-muted-foreground">到期时间</dt>
                          <dd className="mt-1 text-foreground">{plan.expiresAt ? formatTime(plan.expiresAt) : '永久'}</dd>
                        </div>
                        <div className="col-span-2">
                          <dt className="text-muted-foreground">激活时间</dt>
                          <dd className="mt-1 text-foreground">{formatTime(plan.activatedAt)}</dd>
                        </div>
                      </dl>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="w-full text-destructive hover:text-destructive"
                        onClick={() => onCancelPlan(plan)}
                      >
                        撤销套餐
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {otherPlans.length > 0 ? (
              <div className="space-y-2 pt-1">
                <p className="text-xs font-medium text-muted-foreground">历史套餐</p>
                <div className="divide-y divide-border rounded-lg border border-border">
                  {otherPlans.map((plan) => {
                    const status = STATUS_CONFIG[plan.status] ?? {
                      label: plan.status,
                      variant: 'secondary' as const,
                    };
                    return (
                      <div key={plan.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
                        <div className="min-w-0">
                          <p className="truncate text-sm">{plan.planName}</p>
                          <p className="text-xs text-muted-foreground">
                            {PLAN_TYPE_LABELS[plan.planType] ?? plan.planType} · {formatTime(plan.activatedAt)}
                          </p>
                        </div>
                        <Badge variant={status.variant} className="shrink-0">{status.label}</Badge>
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </>
        )}
      </section>

      <section className="space-y-3 border-t border-border pt-5">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-sm font-medium">API Keys</h3>
          {detail ? <span className="text-xs text-muted-foreground">{detail.apiKeys.length} 个</span> : null}
        </div>
        {!detail ? (
          <p className="py-4 text-sm text-muted-foreground">正在加载…</p>
        ) : detail.apiKeys.length === 0 ? (
          <p className="rounded-lg border border-dashed border-border py-5 text-center text-sm text-muted-foreground">暂无 API Key</p>
        ) : (
          <div className="divide-y divide-border rounded-lg border border-border">
            {detail.apiKeys.map((key) => {
              const status = API_KEY_STATUS_CONFIG[key.status];
              return (
                <div key={key.id} className="grid gap-2 px-3 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <p className="truncate text-sm font-medium">{key.name}</p>
                      <Badge variant={status.variant}>{status.label}</Badge>
                    </div>
                    <p className="mt-1 font-mono text-xs text-muted-foreground">{key.keyPrefix}…{key.keySuffix}</p>
                  </div>
                  <p className="text-xs text-muted-foreground sm:text-right">最近使用 {formatTime(key.lastUsedAt)}</p>
                </div>
              );
            })}
          </div>
        )}
      </section>

      <section className="space-y-3 border-t border-border pt-5">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-sm font-medium">最近请求</h3>
          {detail ? <span className="text-xs text-muted-foreground">共 {detail.totalRequests.toLocaleString()} 次</span> : null}
        </div>
        {!detail ? (
          <p className="py-4 text-sm text-muted-foreground">正在加载…</p>
        ) : detail.recentRequests.length === 0 ? (
          <p className="rounded-lg border border-dashed border-border py-5 text-center text-sm text-muted-foreground">暂无请求记录</p>
        ) : (
          <div className="divide-y divide-border rounded-lg border border-border">
            {detail.recentRequests.map((request) => {
              const status = REQUEST_STATUS_CONFIG[request.status];
              return (
                <Link
                  key={request.requestId}
                  href={`/admin/request-logs/${request.requestId}`}
                  className="flex items-center justify-between gap-3 px-3 py-3 transition-colors hover:bg-accent/50"
                >
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{request.model}</p>
                    <p className="mt-0.5 text-xs text-muted-foreground">{formatTime(request.createdAt)}</p>
                  </div>
                  <Badge variant={status.variant} className="shrink-0">{status.label}</Badge>
                </Link>
              );
            })}
          </div>
        )}
      </section>

      <section className="space-y-3 border-t border-border pt-5">
        <h3 className="text-sm font-medium">管理操作</h3>
        <div className="grid gap-2 sm:grid-cols-2">
          <Button variant="outline" onClick={() => onToggleStatus(displayedUser)}>
            {displayedUser.status === 'active' ? '禁用用户' : '启用用户'}
          </Button>
          <Button variant="outline" onClick={() => onToggleRole(displayedUser)} disabled={updating}>
            {displayedUser.role === 'admin' ? '取消管理员' : '设为管理员'}
          </Button>
        </div>
      </section>
    </div>
  );
}

export default function AdminUsersPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [roleF, setRoleF] = useState<string | undefined>(undefined);
  const [statusF, setStatusF] = useState<string | undefined>(undefined);
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ['admin', 'users', page, search, statusF, roleF],
    queryFn: () => adminApi.listUsers(page, 20, search, statusF ?? '', roleF ?? ''),
  });

  const { data: dashboardStats } = useQuery({
    queryKey: ['admin', 'dashboard-stats'],
    queryFn: () => adminApi.getDashboardStats(),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: { status?: 'active' | 'disabled'; role?: 'user' | 'admin' } }) =>
      adminApi.updateUser(id, input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] }),
    onError: () => toast.error('操作失败'),
  });

  const users = data?.users ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 20));

  const [confirm, setConfirm] = useState<{
    open: boolean;
    title: string;
    description: string;
    destructive: boolean;
    onConfirm: () => void;
  }>({ open: false, title: '', description: '', destructive: false, onConfirm: () => {} });

  const [actionUser, setActionUser] = useState<AdminUser | null>(null);
  const [detailSurface, setDetailSurface] = useState<'modal' | 'sheet'>('sheet');
  const [assignOpen, setAssignOpen] = useState(false);
  const [assignForm, setAssignForm] = useState({ planId: '', expiresAt: '' });

  const { data: userDetail } = useQuery({
    queryKey: ['admin', 'user-detail', actionUser?.id],
    queryFn: () => adminApi.getUser(actionUser!.id),
    enabled: !!actionUser,
  });

  const { data: availablePlans = [] } = useQuery({
    queryKey: ['admin', 'plans'],
    queryFn: adminPlanApi.list,
    enabled: assignOpen,
  });

  const assignMutation = useMutation({
    mutationFn: (input: { userId: string; planId: string; expiresAt: string | null }) =>
      adminUserPlanApi.assign(input),
    onSuccess: () => {
      toast.success('套餐已分配');
      setAssignOpen(false);
      void queryClient.invalidateQueries({ queryKey: ['admin', 'user-detail', actionUser?.id] });
    },
    onError: () => toast.error('分配失败'),
  });

  const cancelPlanMutation = useMutation({
    mutationFn: (id: string) => adminUserPlanApi.cancel(id),
    onSuccess: () => {
      toast.success('套餐已撤销');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'user-detail', actionUser?.id] });
    },
    onError: () => toast.error('撤销失败'),
  });

  function handleToggleStatus(user: AdminUser) {
    const next = user.status === 'active' ? 'disabled' : 'active';
    const label = next === 'disabled' ? '禁用' : '启用';
    setConfirm({
      open: true,
      title: `${label}用户`,
      description: `确定要${label}用户 ${user.email} 吗？`,
      destructive: next === 'disabled',
      onConfirm: () => {
        setConfirm((current) => ({ ...current, open: false }));
        updateMutation.mutate(
          { id: user.id, input: { status: next } },
          { onSuccess: () => { toast.success(`用户已${label}`); setActionUser(null); } },
        );
      },
    });
  }

  function handleToggleRole(user: AdminUser) {
    const next = user.role === 'admin' ? 'user' : 'admin';
    const label = next === 'admin' ? '设为管理员' : '取消管理员';
    updateMutation.mutate(
      { id: user.id, input: { role: next } },
      { onSuccess: () => { toast.success(`已${label}`); setActionUser(null); } },
    );
  }

  function handleAssignPlan() {
    if (!actionUser || !assignForm.planId) return;
    assignMutation.mutate({
      userId: actionUser.id,
      planId: assignForm.planId,
      expiresAt: assignForm.expiresAt ? new Date(assignForm.expiresAt).toISOString() : null,
    });
  }

  function handleCancelPlan(plan: AdminUserPlan) {
    setConfirm({
      open: true,
      title: '撤销套餐',
      description: `确定要撤销用户 ${actionUser?.email} 的套餐「${plan.planName}」吗？撤销后用户将失去该套餐权益。`,
      destructive: true,
      onConfirm: () => {
        setConfirm((current) => ({ ...current, open: false }));
        cancelPlanMutation.mutate(plan.id);
      },
    });
  }

  function openAssignDialog() {
    setAssignForm({ planId: '', expiresAt: '' });
    setAssignOpen(true);
  }

  const detailBody = actionUser ? (
    <UserDetailBody
      user={actionUser}
      detail={userDetail}
      updating={updateMutation.isPending}
      onAssignPlan={openAssignDialog}
      onCancelPlan={handleCancelPlan}
      onToggleStatus={handleToggleStatus}
      onToggleRole={handleToggleRole}
    />
  ) : null;

  return (
    <div className="space-y-5">
      <PageHeader
        title="用户管理"
        description="管理注册用户、角色、状态与套餐。"
      />

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-y border-border py-2.5 text-sm">
        <span>共 <strong className="font-semibold tabular-nums">{total}</strong> 位用户</span>
        {dashboardStats ? (
          <span className="text-muted-foreground">
            今日活跃 <strong className="font-semibold text-foreground tabular-nums">{dashboardStats.todayActiveUsers}</strong>
          </span>
        ) : null}
      </div>

      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <input
          type="search"
          placeholder="搜索邮箱"
          aria-label="搜索邮箱"
          value={search}
          onChange={(event) => { setSearch(event.target.value); setPage(1); }}
          className={`${inputCls} flex-1 lg:max-w-md`}
        />
        <div className="flex flex-wrap items-center gap-1.5">
          <FilterChip label="全部" active={!statusF} onClick={() => setStatusF(undefined)} />
          <FilterChip label="正常" active={statusF === 'active'} onClick={() => setStatusF('active')} />
          <FilterChip label="已禁用" active={statusF === 'disabled'} onClick={() => setStatusF('disabled')} />
          <span className="mx-1 hidden h-4 w-px bg-border sm:block" aria-hidden="true" />
          <FilterChip label="管理员" active={roleF === 'admin'} onClick={() => setRoleF(roleF === 'admin' ? undefined : 'admin')} />
          <FilterChip label="普通用户" active={roleF === 'user'} onClick={() => setRoleF(roleF === 'user' ? undefined : 'user')} />
        </div>
      </div>

      <div className="hidden overflow-hidden rounded-lg border border-border bg-card md:block">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30 hover:bg-muted/30">
                <TableHead className="text-xs">邮箱</TableHead>
                <TableHead className="text-xs">角色</TableHead>
                <TableHead className="text-xs">状态</TableHead>
                <TableHead className="text-xs">注册时间</TableHead>
                <TableHead className="text-right text-xs">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell>
                    <Link href={`/admin/users/${user.id}`} className="text-sm font-medium hover:underline">
                      {user.email}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                      {user.role === 'admin' ? '管理员' : '普通用户'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.status === 'active' ? 'success' : 'destructive'}>
                      {user.status === 'active' ? '正常' : '已禁用'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(user.createdAt)}</TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setDetailSurface('modal');
                        setActionUser(user);
                      }}
                    >
                      详情
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {users.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">暂无用户数据</TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className="space-y-2 md:hidden">
        {users.map((user) => (
          <button
            key={user.id}
            type="button"
            onClick={() => {
              setDetailSurface('sheet');
              setActionUser(user);
            }}
            className="w-full space-y-3 rounded-lg border border-border bg-card p-3.5 text-left transition-colors active:bg-accent/50"
          >
            <div className="flex items-start justify-between gap-3">
              <span className="min-w-0 truncate text-sm font-medium">{user.email}</span>
              <Badge variant={user.status === 'active' ? 'success' : 'destructive'} className="shrink-0">
                {user.status === 'active' ? '正常' : '已禁用'}
              </Badge>
            </div>
            <div className="flex items-center justify-between gap-3">
              <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                {user.role === 'admin' ? '管理员' : '普通用户'}
              </Badge>
              <span className="text-xs text-muted-foreground">{formatTime(user.createdAt)}</span>
            </div>
          </button>
        ))}
        {users.length === 0 ? <p className="py-10 text-center text-sm text-muted-foreground">暂无用户数据</p> : null}
      </div>

      <div className="flex items-center justify-between gap-4 border-t border-border pt-3 text-sm">
        <p className="text-xs text-muted-foreground">共 {total} 条</p>
        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon"
            onClick={() => setPage((current) => Math.max(1, current - 1))}
            disabled={page <= 1}
            aria-label="上一页"
            className="size-8"
          >
            <ChevronLeft className="size-3.5" />
          </Button>
          <span className="min-w-14 px-2 text-center text-xs tabular-nums">{page} / {totalPages}</span>
          <Button
            variant="outline"
            size="icon"
            onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
            disabled={page >= totalPages}
            aria-label="下一页"
            className="size-8"
          >
            <ChevronRight className="size-3.5" />
          </Button>
        </div>
      </div>

      <Modal
        open={!!actionUser && detailSurface === 'modal'}
        onClose={() => setActionUser(null)}
        title={actionUser?.email ?? '用户详情'}
        description="用户详情与管理操作"
        maxWidth="2xl"
      >
        {detailBody}
      </Modal>

      <BottomSheet
        open={!!actionUser && detailSurface === 'sheet'}
        onOpenChange={(open) => !open && setActionUser(null)}
      >
        {actionUser ? (
          <div className="px-4 pb-6">
            <div className="mb-5 border-b border-border pb-4">
              <h2 className="truncate text-base font-semibold">{actionUser.email}</h2>
              <p className="mt-1 text-sm text-muted-foreground">用户详情与管理操作</p>
            </div>
            {detailBody}
          </div>
        ) : null}
      </BottomSheet>

      <Dialog open={assignOpen} onOpenChange={setAssignOpen}>
        <DialogHeader>
          <DialogTitle>分配套餐</DialogTitle>
          <DialogDescription>为用户 {actionUser?.email} 分配新套餐</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <label className="text-sm font-medium">选择套餐</label>
            <select
              className={inputCls}
              value={assignForm.planId}
              onChange={(event) => setAssignForm((current) => ({ ...current, planId: event.target.value }))}
            >
              <option value="">请选择套餐</option>
              {availablePlans.filter((plan) => plan.status === 'active').map((plan) => (
                <option key={plan.id} value={plan.id}>
                  {plan.name} ({PLAN_TYPE_LABELS[plan.planType] ?? plan.planType})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">到期时间（选填）</label>
            <input
              type="datetime-local"
              className={inputCls}
              value={assignForm.expiresAt}
              onChange={(event) => setAssignForm((current) => ({ ...current, expiresAt: event.target.value }))}
            />
            <p className="text-xs text-muted-foreground">留空表示永久有效</p>
          </div>
          <div className="flex gap-2 pt-2">
            <Button variant="outline" className="flex-1" onClick={() => setAssignOpen(false)}>取消</Button>
            <Button
              className="flex-1"
              onClick={handleAssignPlan}
              disabled={!assignForm.planId || assignMutation.isPending}
            >
              {assignMutation.isPending ? '分配中…' : '确认分配'}
            </Button>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirm.open}
        onOpenChange={(open) => setConfirm((current) => ({ ...current, open }))}
        title={confirm.title}
        description={confirm.description}
        destructive={confirm.destructive}
        loading={updateMutation.isPending || cancelPlanMutation.isPending}
        onConfirm={confirm.onConfirm}
      />
    </div>
  );
}

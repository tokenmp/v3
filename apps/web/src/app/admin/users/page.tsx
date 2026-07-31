'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi, adminPlanApi, adminUserPlanApi } from '@/lib/api/admin';
import { Button } from '@/components/ui/button';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { FilterChip } from '@/components/filter-chip';
import { ChevronLeft, ChevronRight, Plus, Package } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminUser, AdminUserPlan } from '@/types/admin';
import {
  Dialog,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { BottomSheet } from '@/components/ui/bottom-sheet';

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

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: { status?: 'active' | 'disabled'; role?: 'user' | 'admin' } }) =>
      adminApi.updateUser(id, input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] }),
    onError: () => toast.error('操作失败'),
  });

  const users = data?.users ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / 20));

  // Confirm dialog state
  const [confirm, setConfirm] = useState<{
    open: boolean;
    title: string;
    description: string;
    destructive: boolean;
    onConfirm: () => void;
  }>({ open: false, title: '', description: '', destructive: false, onConfirm: () => {} });

  // Mobile action sheet state
  const [actionUser, setActionUser] = useState<AdminUser | null>(null);

  // Plan assignment dialog state
  const [assignOpen, setAssignOpen] = useState(false);
  const [assignForm, setAssignForm] = useState({ planId: '', expiresAt: '' });

  // Fetch user detail (with plans) when actionUser is set
  const { data: userDetail } = useQuery({
    queryKey: ['admin', 'user-detail', actionUser?.id],
    queryFn: () => adminApi.getUser(actionUser!.id),
    enabled: !!actionUser,
  });

  // Fetch available plans for assignment
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
        setConfirm((c) => ({ ...c, open: false }));
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
        setConfirm((c) => ({ ...c, open: false }));
        cancelPlanMutation.mutate(plan.id);
      },
    });
  }

  const userPlans = (userDetail?.userPlans ?? []) as unknown as AdminUserPlan[];
  const activePlans = userPlans.filter((p) => p.status === 'active');
  const otherPlans = userPlans.filter((p) => p.status !== 'active');

  return (
    <div className="space-y-4">
      {/* 工具栏：搜索框左 + 筛选 chip 右 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-2">
          <input
            type="text"
            placeholder="搜索邮箱"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setPage(1); }}
            className="h-[var(--control-height-sm)] min-w-40 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
        <div className="flex flex-wrap gap-1.5 text-xs">
          <FilterChip label="全部" active={!statusF} onClick={() => setStatusF(undefined)} />
          <FilterChip label="正常" active={statusF === 'active'} onClick={() => setStatusF('active')} />
          <FilterChip label="已禁用" active={statusF === 'disabled'} onClick={() => setStatusF('disabled')} />
          <span className="mx-1 self-center text-muted-foreground">|</span>
          <FilterChip label="管理员" active={roleF === 'admin'} onClick={() => setRoleF(roleF === 'admin' ? undefined : 'admin')} />
          <FilterChip label="普通用户" active={roleF === 'user'} onClick={() => setRoleF(roleF === 'user' ? undefined : 'user')} />
        </div>
      </div>

      {/* 表格 */}
      <div className="hidden md:block rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead className="text-xs">邮箱</TableHead>
                <TableHead className="text-xs">角色</TableHead>
                <TableHead className="text-xs">状态</TableHead>
                <TableHead className="text-xs">注册时间</TableHead>
                <TableHead className="text-xs">操作</TableHead>
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
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${user.role === 'admin' ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
                      {user.role === 'admin' ? '管理员' : '用户'}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.status === 'active' ? 'success' : 'destructive'} className="text-[10px]">
                      {user.status === 'active' ? '正常' : '已禁用'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(user.createdAt)}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <Button variant="ghost" size="sm" onClick={() => handleToggleStatus(user)}>
                        {user.status === 'active' ? '禁用' : '启用'}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleToggleRole(user)}
                        disabled={updateMutation.isPending}
                      >
                        {user.role === 'admin' ? '取消管理员' : '设为管理员'}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {users.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                    暂无用户数据
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {users.map((user) => (
          <button
            key={user.id}
            type="button"
            onClick={() => setActionUser(user)}
            className="w-full text-left rounded-lg border bg-card p-3 space-y-2 active:bg-accent/50 transition-colors"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium truncate">{user.email}</span>
              <Badge variant={user.status === 'active' ? 'success' : 'destructive'} className="shrink-0 text-[10px]">
                {user.status === 'active' ? '正常' : '已禁用'}
              </Badge>
            </div>
            <div className="flex items-center justify-between">
              <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${user.role === 'admin' ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
                {user.role === 'admin' ? '管理员' : '用户'}
              </span>
              <span className="text-xs text-muted-foreground">{formatTime(user.createdAt)}</span>
            </div>
          </button>
        ))}
        {users.length === 0 && (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无用户数据</p>
        )}
      </div>

      {/* Mobile action sheet */}
      <BottomSheet open={!!actionUser} onOpenChange={(open) => !open && setActionUser(null)}>
        {actionUser && (
          <div className="px-4 pb-6 max-h-[80vh] overflow-y-auto">
            <div className="mb-4">
              <h3 className="font-medium">{actionUser.email}</h3>
              <p className="text-sm text-muted-foreground">用户详情与操作</p>
            </div>
            <div className="space-y-4">
              {/* User info */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-sm text-muted-foreground">角色</span>
                  <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${actionUser.role === 'admin' ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
                    {actionUser.role === 'admin' ? '管理员' : '用户'}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-muted-foreground">状态</span>
                  <Badge variant={actionUser.status === 'active' ? 'success' : 'destructive'} className="text-[10px]">
                    {actionUser.status === 'active' ? '正常' : '已禁用'}
                  </Badge>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-muted-foreground">注册时间</span>
                  <span className="text-sm">{formatTime(actionUser.createdAt)}</span>
                </div>
              </div>

              {/* Plans section */}
              <div className="pt-3 border-t">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <Package className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm font-medium">套餐</span>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => {
                      setAssignForm({ planId: '', expiresAt: '' });
                      setAssignOpen(true);
                    }}
                  >
                    <Plus className="h-3 w-3 mr-1" />
                    分配
                  </Button>
                </div>

                {/* Active plans */}
                {activePlans.length > 0 && (
                  <div className="space-y-2 mb-3">
                    <p className="text-xs text-muted-foreground">当前套餐</p>
                    {activePlans.map((plan) => (
                      <div key={plan.id} className="rounded-lg border p-3 space-y-2">
                        <div className="flex items-center justify-between">
                          <span className="text-sm font-medium">{plan.planName}</span>
                          <Badge variant="success" className="text-[10px]">有效</Badge>
                        </div>
                        <div className="grid grid-cols-2 gap-2 text-xs">
                          <div>
                            <span className="text-muted-foreground">类型</span>
                            <p>{PLAN_TYPE_LABELS[plan.planType] ?? plan.planType}</p>
                          </div>
                          <div>
                            <span className="text-muted-foreground">剩余额度</span>
                            <p className="font-mono">{Number(plan.remainingQuota).toLocaleString()}</p>
                          </div>
                          <div>
                            <span className="text-muted-foreground">到期时间</span>
                            <p>{plan.expiresAt ? formatTime(plan.expiresAt) : '永久'}</p>
                          </div>
                          <div>
                            <span className="text-muted-foreground">激活时间</span>
                            <p>{formatTime(plan.activatedAt)}</p>
                          </div>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="w-full text-destructive hover:text-destructive h-8"
                          onClick={() => handleCancelPlan(plan)}
                        >
                          撤销套餐
                        </Button>
                      </div>
                    ))}
                  </div>
                )}

                {/* No active plans */}
                {activePlans.length === 0 && (
                  <div className="rounded-lg border border-dashed p-4 text-center text-sm text-muted-foreground">
                    暂无有效套餐
                  </div>
                )}

                {/* Historical plans */}
                {otherPlans.length > 0 && (
                  <div className="mt-3">
                    <p className="text-xs text-muted-foreground mb-2">历史套餐</p>
                    <div className="space-y-2">
                      {otherPlans.map((plan) => {
                        const statusCfg = STATUS_CONFIG[plan.status] ?? { label: plan.status, variant: 'secondary' as const };
                        return (
                          <div key={plan.id} className="rounded-lg bg-muted/50 p-2.5 flex items-center justify-between">
                            <div className="min-w-0">
                              <p className="text-sm truncate">{plan.planName}</p>
                              <p className="text-[10px] text-muted-foreground">
                                {PLAN_TYPE_LABELS[plan.planType] ?? plan.planType} · {formatTime(plan.activatedAt)}
                              </p>
                            </div>
                            <Badge variant={statusCfg.variant} className="text-[10px] shrink-0 ml-2">
                              {statusCfg.label}
                            </Badge>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                )}
              </div>

              {/* Actions */}
              <div className="space-y-2 pt-3 border-t">
                <Button
                  variant="outline"
                  className="w-full"
                  onClick={() => handleToggleStatus(actionUser)}
                >
                  {actionUser.status === 'active' ? '禁用用户' : '启用用户'}
                </Button>
                <Button
                  variant="outline"
                  className="w-full"
                  onClick={() => handleToggleRole(actionUser)}
                  disabled={updateMutation.isPending}
                >
                  {actionUser.role === 'admin' ? '取消管理员' : '设为管理员'}
                </Button>
              </div>
            </div>
          </div>
        )}
      </BottomSheet>

      {/* Assign plan dialog */}
      <Dialog open={assignOpen} onOpenChange={setAssignOpen}>
        <DialogHeader>
          <DialogTitle>分配套餐</DialogTitle>
          <DialogDescription>
            为用户 {actionUser?.email} 分配新套餐
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <label className="text-sm font-medium">选择套餐</label>
            <select
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={assignForm.planId}
              onChange={(e) => setAssignForm((f) => ({ ...f, planId: e.target.value }))}
            >
              <option value="">请选择套餐</option>
              {availablePlans.filter((p) => p.status === 'active').map((p) => (
                <option key={p.id} value={p.id}>{p.name} ({PLAN_TYPE_LABELS[p.planType] ?? p.planType})</option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">到期时间（选填）</label>
            <input
              type="datetime-local"
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={assignForm.expiresAt}
              onChange={(e) => setAssignForm((f) => ({ ...f, expiresAt: e.target.value }))}
            />
            <p className="text-xs text-muted-foreground">留空表示永久有效</p>
          </div>
          <div className="flex gap-2 pt-2">
            <Button variant="outline" className="flex-1" onClick={() => setAssignOpen(false)}>
              取消
            </Button>
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

      {/* 分页 */}
      <div className="flex items-center justify-between gap-4 px-1 py-1 text-sm">
        <p className="text-xs text-muted-foreground">共 {total} 条</p>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="rounded-sm border border-border p-1.5 text-muted-foreground hover:bg-accent disabled:opacity-40 disabled:hover:bg-transparent"
            aria-label="上一页"
          >
            <ChevronLeft className="size-3.5" />
          </button>
          <span className="px-2 text-xs tabular-nums">{page} / {totalPages}</span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            className="rounded-sm border border-border p-1.5 text-muted-foreground hover:bg-accent disabled:opacity-40 disabled:hover:bg-transparent"
            aria-label="下一页"
          >
            <ChevronRight className="size-3.5" />
          </button>
        </div>
      </div>

      <ConfirmDialog
        open={confirm.open}
        onOpenChange={(open) => setConfirm((c) => ({ ...c, open }))}
        title={confirm.title}
        description={confirm.description}
        destructive={confirm.destructive}
        loading={updateMutation.isPending || cancelPlanMutation.isPending}
        onConfirm={confirm.onConfirm}
      />
    </div>
  );
}

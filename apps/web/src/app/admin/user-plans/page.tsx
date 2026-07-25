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
import { Plus, Ban } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminUserPlanInput } from '@/types/admin';

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

const emptyForm: AssignForm = {
  userId: '',
  planId: '',
  expiresAt: '',
};

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

  // Assign dialog
  const [assignOpen, setAssignOpen] = useState(false);
  const [form, setForm] = useState<AssignForm>(emptyForm);

  // Cancel confirm
  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancellingId, setCancellingId] = useState<string | null>(null);

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
                      <TableCell className="font-medium">{up.userEmail}</TableCell>
                      <TableCell>{up.planName}</TableCell>
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
                          <Button
                            variant="ghost"
                            size="icon"
                            className="text-destructive"
                            onClick={() => openCancel(up.id)}
                          >
                            <Ban className="h-4 w-4" />
                          </Button>
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
                        <div className="font-medium">{up.userEmail}</div>
                        <div className="text-sm text-muted-foreground">{up.planName}</div>
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
                      <div className="flex justify-end">
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

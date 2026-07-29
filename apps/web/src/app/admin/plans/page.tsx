'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminPlanApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
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
import { Plus, Pencil, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminPlan, AdminPlanInput } from '@/types/admin';

const PLAN_TYPE_OPTIONS: { value: AdminPlanInput['planType']; label: string }[] = [
  { value: 'coding', label: '编程' },
  { value: 'token', label: 'Token' },
  { value: 'image', label: '图像' },
  { value: 'free', label: '免费' },
];

const CATEGORY_OPTIONS: { value: AdminPlanInput['category']; label: string }[] = [
  { value: 'daily', label: '天' },
  { value: 'weekly', label: '周' },
  { value: 'monthly', label: '月' },
  { value: 'quarterly', label: '季' },
  { value: 'yearly', label: '年' },
];

const STATUS_OPTIONS: { value: AdminPlanInput['status']; label: string; badgeVariant: 'success' | 'secondary' }[] = [
  { value: 'active', label: '启用', badgeVariant: 'success' },
  { value: 'disabled', label: '禁用', badgeVariant: 'secondary' },
];

function planTypeLabel(t: AdminPlan['planType']) {
  return PLAN_TYPE_OPTIONS.find((o) => o.value === t)?.label ?? t;
}

function categoryLabel(c: AdminPlan['category']) {
  return CATEGORY_OPTIONS.find((o) => o.value === c)?.label ?? c;
}

function formatQuota(plan: AdminPlan) {
  if (plan.tokenLimit !== null) return `${plan.tokenLimit} tokens`;
  if (plan.monthlyLimit !== null) return `${plan.monthlyLimit} 次/月`;
  return '—';
}

function formatModels(models: string[]) {
  if (models.length === 0) return '—';
  if (models.length <= 3) return models.join(', ');
  return `${models.slice(0, 2).join(', ')} …`;
}

interface FormState {
  name: string;
  planType: AdminPlanInput['planType'];
  price: number;
  category: AdminPlanInput['category'];
  monthlyLimit: number | null;
  tokenLimit: number | null;
  allowedModels: string[];
  status: AdminPlanInput['status'];
}

const emptyForm: FormState = {
  name: '',
  planType: 'token',
  price: 0,
  category: 'monthly',
  monthlyLimit: null,
  tokenLimit: null,
  allowedModels: [],
  status: 'active',
};

function planToForm(p: AdminPlan): FormState {
  return {
    name: p.name,
    planType: p.planType,
    price: p.price,
    category: p.category,
    monthlyLimit: p.monthlyLimit,
    tokenLimit: p.tokenLimit,
    allowedModels: [...p.allowedModels],
    status: p.status as AdminPlanInput['status'],
  };
}

export default function AdminPlansPage() {
  const qc = useQueryClient();

  const { data: plans = [], isLoading } = useQuery({
    queryKey: ['admin', 'plans'],
    queryFn: adminPlanApi.list,
  });

  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'models-catalog'],
    queryFn: adminPlanApi.listModels,
  });

  const createMutation = useMutation({
    mutationFn: (input: AdminPlanInput) => adminPlanApi.create(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'plans'] });
      toast.success('套餐已创建');
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: AdminPlanInput }) =>
      adminPlanApi.update(id, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'plans'] });
      toast.success('套餐已更新');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminPlanApi.delete(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'plans'] });
      toast.success('套餐已删除');
    },
  });

  // Dialog state
  const [formOpen, setFormOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<AdminPlan | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);

  // Delete confirm
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  function openCreate() {
    setEditingItem(null);
    setForm(emptyForm);
    setFormOpen(true);
  }

  function openEdit(item: AdminPlan) {
    setEditingItem(item);
    setForm(planToForm(item));
    setFormOpen(true);
  }

  function handleSubmit() {
    if (!form.name.trim()) {
      toast.error('请填写套餐名称');
      return;
    }
    const input: AdminPlanInput = {
      name: form.name.trim(),
      planType: form.planType,
      price: form.price,
      category: form.category,
      monthlyLimit: form.monthlyLimit,
      tokenLimit: form.tokenLimit,
      allowedModels: form.allowedModels,
      status: form.status,
    };
    if (editingItem) {
      updateMutation.mutate({ id: editingItem.id, input }, { onSuccess: () => setFormOpen(false) });
    } else {
      createMutation.mutate(input, { onSuccess: () => setFormOpen(false) });
    }
  }

  function openDelete(id: string) {
    setDeletingId(id);
    setDeleteOpen(true);
  }

  function handleDelete() {
    if (!deletingId) return;
    deleteMutation.mutate(deletingId, { onSuccess: () => setDeleteOpen(false) });
  }

  function toggleModel(model: string, checked: boolean) {
    setForm((f) => ({
      ...f,
      allowedModels: checked
        ? [...f.allowedModels, model]
        : f.allowedModels.filter((m) => m !== model),
    }));
  }

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      {/* Top bar */}
      <div className="flex justify-end">
        <Button onClick={openCreate}>
          <Plus />
          新建套餐
        </Button>
      </div>

      {plans.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无套餐</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>价格</TableHead>
                  <TableHead>周期</TableHead>
                  <TableHead>额度</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {plans.map((p) => {
                  const statusOpt = STATUS_OPTIONS.find((s) => s.value === p.status);
                  return (
                    <TableRow key={p.id}>
                      <TableCell className="font-medium">{p.name}</TableCell>
                      <TableCell>
                        <Badge variant="default">{planTypeLabel(p.planType)}</Badge>
                      </TableCell>
                      <TableCell>{p.price === 0 ? '免费' : `¥${p.price}`}</TableCell>
                      <TableCell>{categoryLabel(p.category)}</TableCell>
                      <TableCell>{formatQuota(p)}</TableCell>
                      <TableCell className="max-w-[200px] truncate" title={p.allowedModels.join(', ')}>
                        {formatModels(p.allowedModels)}
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusOpt?.badgeVariant ?? 'secondary'}>
                          {statusOpt?.label ?? p.status}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button variant="ghost" size="icon" onClick={() => openEdit(p)}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive"
                          onClick={() => openDelete(p.id)}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {plans.map((p) => {
              const statusOpt = STATUS_OPTIONS.find((s) => s.value === p.status);
              return (
                <Card key={p.id}>
                  <CardContent className="p-4 space-y-3">
                    <div className="flex items-start justify-between gap-2">
                      <span className="font-medium">{p.name}</span>
                      <div className="flex items-center gap-1.5 shrink-0">
                        <Badge variant="default">{planTypeLabel(p.planType)}</Badge>
                        <Badge variant={statusOpt?.badgeVariant ?? 'secondary'}>
                          {statusOpt?.label ?? p.status}
                        </Badge>
                      </div>
                    </div>
                    <div className="text-sm text-muted-foreground space-y-1">
                      <p>{p.price === 0 ? '免费' : `¥${p.price}`} · {categoryLabel(p.category)}</p>
                      <p>额度：{formatQuota(p)}</p>
                      <p>模型：{p.allowedModels.length > 0 ? `${p.allowedModels.length} 个` : '—'}</p>
                    </div>
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="icon" onClick={() => openEdit(p)}>
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive"
                        onClick={() => openDelete(p.id)}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        </>
      )}

      {/* Create / Edit Dialog */}
      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogHeader>
          <DialogTitle>{editingItem ? '编辑套餐' : '新建套餐'}</DialogTitle>
          <DialogDescription>
            {editingItem ? '修改套餐信息后保存。' : '填写套餐信息并创建。'}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {/* Name */}
          <div className="space-y-2">
            <Label htmlFor="plan-name">名称</Label>
            <Input
              id="plan-name"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="套餐名称"
              required
            />
          </div>

          {/* Plan type */}
          <div className="space-y-2">
            <Label>类型</Label>
            <div className="flex flex-wrap gap-2">
              {PLAN_TYPE_OPTIONS.map((opt) => (
                <Button
                  key={opt.value}
                  type="button"
                  variant={form.planType === opt.value ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => setForm((f) => ({ ...f, planType: opt.value }))}
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>

          {/* Price */}
          <div className="space-y-2">
            <Label htmlFor="plan-price">价格</Label>
            <Input
              id="plan-price"
              type="number"
              min={0}
              value={form.price}
              onChange={(e) => setForm((f) => ({ ...f, price: Number(e.target.value) || 0 }))}
            />
          </div>

          {/* Category */}
          <div className="space-y-2">
            <Label>周期</Label>
            <div className="flex gap-2">
              {CATEGORY_OPTIONS.map((opt) => (
                <Button
                  key={opt.value}
                  type="button"
                  variant={form.category === opt.value ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => setForm((f) => ({ ...f, category: opt.value }))}
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>

          {/* Monthly limit (for coding) */}
          <div className="space-y-2">
            <Label htmlFor="plan-monthly-limit">月次数额度（选填，编程类型用）</Label>
            <Input
              id="plan-monthly-limit"
              type="number"
              min={0}
              placeholder="如 1000"
              value={form.monthlyLimit ?? ''}
              onChange={(e) => {
                const v = e.target.value;
                setForm((f) => ({ ...f, monthlyLimit: v === '' ? null : Number(v) || null }));
              }}
            />
          </div>

          {/* Token limit (for token) */}
          <div className="space-y-2">
            <Label htmlFor="plan-token-limit">Token 额度（选填，Token 类型用）</Label>
            <Input
              id="plan-token-limit"
              type="number"
              min={0}
              placeholder="如 500000"
              value={form.tokenLimit ?? ''}
              onChange={(e) => {
                const v = e.target.value;
                setForm((f) => ({ ...f, tokenLimit: v === '' ? null : Number(v) || null }));
              }}
            />
          </div>

          {/* Allowed models */}
          <div className="space-y-2">
            <Label>允许模型</Label>
            {models.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无可选模型</p>
            ) : (
              <div className="grid grid-cols-2 gap-2">
                {models.map((model) => (
                  <label
                    key={model}
                    className="flex items-center gap-2 text-sm cursor-pointer"
                  >
                    <Checkbox
                      checked={form.allowedModels.includes(model)}
                      onCheckedChange={(checked) => toggleModel(model, !!checked)}
                    />
                    <span className="truncate" title={model}>{model}</span>
                  </label>
                ))}
              </div>
            )}
          </div>

          {/* Status */}
          <div className="space-y-2">
            <Label>状态</Label>
            <div className="flex gap-2">
              {STATUS_OPTIONS.map((opt) => (
                <Button
                  key={opt.value}
                  type="button"
                  variant={form.status === opt.value ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => setForm((f) => ({ ...f, status: opt.value }))}
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setFormOpen(false)} disabled={isSubmitting}>
            取消
          </Button>
          <Button onClick={handleSubmit} disabled={isSubmitting}>
            {isSubmitting ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Delete Confirm */}
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="删除套餐"
        description="确定要删除此套餐吗？此操作不可撤销。"
        confirmText="删除"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={handleDelete}
      />
    </div>
  );
}

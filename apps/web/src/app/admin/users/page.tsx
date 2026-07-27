'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import { Button } from '@/components/ui/button';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { FilterChip } from '@/components/filter-chip';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminUser } from '@/types/admin';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function AdminUsersPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [roleF, setRoleF] = useState<string | undefined>(undefined);
  const [statusF, setStatusF] = useState<string | undefined>(undefined);
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ['admin', 'users', page, search],
    queryFn: () => adminApi.listUsers(page, 20, search),
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

  const filtered = users.filter((u) => {
    if (roleF && u.role !== roleF) return false;
    if (statusF && u.status !== statusF) return false;
    return true;
  });

  const [confirm, setConfirm] = useState<{
    open: boolean;
    title: string;
    description: string;
    destructive: boolean;
    onConfirm: () => void;
  }>({ open: false, title: '', description: '', destructive: false, onConfirm: () => {} });

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
          { onSuccess: () => toast.success(`用户已${label}`) },
        );
      },
    });
  }

  function handleToggleRole(user: AdminUser) {
    const next = user.role === 'admin' ? 'user' : 'admin';
    const label = next === 'admin' ? '设为管理员' : '取消管理员';
    updateMutation.mutate(
      { id: user.id, input: { role: next } },
      { onSuccess: () => toast.success(`已${label}`) },
    );
  }

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">用户管理</h1>

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
              {filtered.map((user) => (
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
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${user.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
                      {user.status === 'active' ? '正常' : '已禁用'}
                    </span>
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
              {filtered.length === 0 && (
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
        {filtered.map((user) => (
          <div key={user.id} className="rounded-lg border bg-card p-3 space-y-2">
            <div className="flex items-center justify-between gap-2">
              <Link href={`/admin/users/${user.id}`} className="text-sm font-medium truncate hover:underline">
                {user.email}
              </Link>
              <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${user.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
                {user.status === 'active' ? '正常' : '已禁用'}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${user.role === 'admin' ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
                {user.role === 'admin' ? '管理员' : '用户'}
              </span>
              <span className="text-xs text-muted-foreground">{formatTime(user.createdAt)}</span>
            </div>
            <div className="flex gap-2 pt-1">
              <Button
                variant="outline"
                size="sm"
                className="flex-1"
                onClick={() => handleToggleStatus(user)}
              >
                {user.status === 'active' ? '禁用' : '启用'}
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="flex-1"
                onClick={() => handleToggleRole(user)}
                disabled={updateMutation.isPending}
              >
                {user.role === 'admin' ? '取消管理员' : '设为管理员'}
              </Button>
            </div>
          </div>
        ))}
        {filtered.length === 0 && (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无用户数据</p>
        )}
      </div>

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
        loading={updateMutation.isPending}
        onConfirm={confirm.onConfirm}
      />
    </div>
  );
}

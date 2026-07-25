'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { ChevronLeft, ChevronRight, Search } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminUser } from '@/types/admin';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function AdminUsersPage() {
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const pageSize = 20;
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ['admin', 'users', page, search],
    queryFn: () => adminApi.listUsers(page, pageSize, search),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: { status?: 'active' | 'disabled'; role?: 'user' | 'admin' } }) =>
      adminApi.updateUser(id, input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'] });
    },
    onError: () => toast.error('操作失败'),
  });

  const users = data?.users ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  // Confirm dialog state
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

  function handleSearchChange(value: string) {
    setSearch(value);
    setPage(1);
  }

  return (
    <div className="space-y-6">
      {/* Search bar */}
      <div className="flex justify-between">
        <div className="relative w-full max-w-sm">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="搜索邮箱"
            value={search}
            onChange={(e) => handleSearchChange(e.target.value)}
            className="pl-9"
          />
        </div>
      </div>

      {/* Desktop table */}
      <div className="hidden md:block">
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>邮箱</TableHead>
                <TableHead>角色</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>注册时间</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell>
                    <Link
                      href={`/admin/users/${user.id}`}
                      className="text-sm font-medium hover:underline"
                    >
                      {user.email}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                      {user.role === 'admin' ? '管理员' : '用户'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.status === 'active' ? 'success' : 'destructive'}>
                      {user.status === 'active' ? '正常' : '已禁用'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(user.createdAt)}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleToggleStatus(user)}
                      >
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
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                    暂无用户数据
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {users.map((user) => (
          <Card key={user.id}>
            <CardContent className="p-4 space-y-3">
              <div className="flex items-center justify-between">
                <Link
                  href={`/admin/users/${user.id}`}
                  className="font-medium text-sm hover:underline"
                >
                  {user.email}
                </Link>
                <Badge variant={user.status === 'active' ? 'success' : 'destructive'}>
                  {user.status === 'active' ? '正常' : '已禁用'}
                </Badge>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                  {user.role === 'admin' ? '管理员' : '用户'}
                </Badge>
                <span className="text-xs text-muted-foreground">
                  {formatTime(user.createdAt)}
                </span>
              </div>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => handleToggleStatus(user)}
                >
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
            </CardContent>
          </Card>
        ))}
        {users.length === 0 && (
          <p className="text-center text-muted-foreground py-8 text-sm">暂无用户数据</p>
        )}
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          共 {total} 条
        </p>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
          >
            <ChevronLeft className="h-4 w-4" />
            上一页
          </Button>
          <span className="text-sm">
            第 {page} / {totalPages} 页
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
          >
            下一页
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Confirm dialog */}
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

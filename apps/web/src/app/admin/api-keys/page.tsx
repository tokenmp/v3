'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import { userApi } from '@/lib/api/user';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Table, TableHeader, TableRow, TableHead, TableBody, TableCell,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { toast } from 'sonner';
import type { AdminApiKey } from '@/types/admin';

function formatTime(iso: string | null) {
  return iso ? new Date(iso).toLocaleString('zh-CN') : '—';
}

export default function AdminApiKeysPage() {
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const [revokeTarget, setRevokeTarget] = useState<AdminApiKey | null>(null);

  const { data } = useQuery({
    queryKey: ['admin', 'keys', page],
    queryFn: () => adminApi.listKeys(page, pageSize),
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => userApi.revokeKey(id),
    onSuccess: () => {
      setRevokeTarget(null);
      queryClient.invalidateQueries({ queryKey: ['admin', 'keys'] });
      toast.success('密钥已撤销');
    },
    onError: () => toast.error('撤销密钥失败'),
  });

  const keys = data?.keys ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="space-y-6">
      {/* Desktop table */}
      <div className="hidden md:block">
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>密钥</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead>最近使用</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.map((k: AdminApiKey) => (
                <TableRow key={k.id}>
                  <TableCell className="text-sm">{k.userEmail}</TableCell>
                  <TableCell className="font-medium text-sm">{k.name}</TableCell>
                  <TableCell className="font-mono text-xs">{k.keyPrefix}…{k.keySuffix}</TableCell>
                  <TableCell>
                    <Badge variant={k.status === 'active' ? 'success' : 'secondary'}>
                      {k.status === 'active' ? '活跃' : '已禁用'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(k.createdAt)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatTime(k.lastUsedAt)}</TableCell>
                  <TableCell>
                    {k.status === 'active' && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => setRevokeTarget(k)}
                      >
                        撤销
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {keys.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    暂无 API 密钥
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {keys.map((k: AdminApiKey) => (
          <Card key={k.id}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-medium text-sm">{k.name}</span>
                <Badge variant={k.status === 'active' ? 'success' : 'secondary'}>
                  {k.status === 'active' ? '活跃' : '已禁用'}
                </Badge>
              </div>
              <p className="text-xs text-muted-foreground">{k.userEmail}</p>
              <p className="font-mono text-xs text-muted-foreground">{k.keyPrefix}…{k.keySuffix}</p>
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>创建：{formatTime(k.createdAt)}</span>
                <span>使用：{formatTime(k.lastUsedAt)}</span>
              </div>
              {k.status === 'active' && (
                <Button
                  variant="outline"
                  size="sm"
                  className="w-full text-destructive hover:text-destructive"
                  onClick={() => setRevokeTarget(k)}
                >
                  撤销密钥
                </Button>
              )}
            </CardContent>
          </Card>
        ))}
        {keys.length === 0 && (
          <p className="text-center text-muted-foreground py-8 text-sm">暂无 API 密钥</p>
        )}
      </div>

      {/* Pagination */}
      {total > pageSize && (
        <div className="flex items-center justify-between">
          <p className="text-sm text-muted-foreground">共 {total} 条</p>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page <= 1}>
              上一页
            </Button>
            <span className="text-sm">第 {page} / {totalPages} 页</span>
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={page >= totalPages}>
              下一页
            </Button>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={!!revokeTarget}
        onOpenChange={(open) => !open && setRevokeTarget(null)}
        title="撤销密钥"
        description={`确定要撤销用户 ${revokeTarget?.userEmail} 的密钥「${revokeTarget?.name}」吗？此操作不可恢复。`}
        confirmText="确认撤销"
        destructive
        loading={revokeMutation.isPending}
        onConfirm={() => revokeTarget && revokeMutation.mutate(revokeTarget.id)}
      />
    </div>
  );
}

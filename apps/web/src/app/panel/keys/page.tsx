'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { userApi } from '@/lib/api/user';
import { copyText } from '@/lib/utils';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
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
import { Plus, Copy, Trash2, RotateCw } from 'lucide-react';
import { toast } from 'sonner';
import type { ApiKey, ApiKeyCreated } from '@/types';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

export default function KeysPage() {
  const queryClient = useQueryClient();

  const { data: keys } = useQuery({
    queryKey: ['keys'],
    queryFn: userApi.getKeys,
  });

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false);
  const [keyName, setKeyName] = useState('');
  const createMutation = useMutation({
    mutationFn: (name: string) => userApi.createKey({ name }),
    onSuccess: (newKey) => {
      setCreateOpen(false);
      setKeyName('');
      setRevealKey(newKey);
      queryClient.invalidateQueries({ queryKey: ['keys'] });
    },
    onError: () => toast.error('创建密钥失败'),
  });

  // Reveal dialog (ApiKeyCreated carries the one-time secret)
  const [revealKey, setRevealKey] = useState<ApiKeyCreated | null>(null);

  // Revoke dialog
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);
  const revokeMutation = useMutation({
    mutationFn: (id: string) => userApi.revokeKey(id),
    onSuccess: () => {
      setRevokeTarget(null);
      queryClient.invalidateQueries({ queryKey: ['keys'] });
      toast.success('密钥已撤销');
    },
    onError: () => toast.error('撤销密钥失败'),
  });

  // Rotate
  const [rotateTarget, setRotateTarget] = useState<ApiKey | null>(null);
  const rotateMutation = useMutation({
    mutationFn: (id: string) => userApi.rotateKey(id),
    onSuccess: (newKey) => {
      setRotateTarget(null);
      setRevealKey(newKey);
      queryClient.invalidateQueries({ queryKey: ['keys'] });
      toast.success('密钥已轮换');
    },
    onError: () => toast.error('轮换密钥失败'),
  });

  const copyToClipboard = async (text: string, label: string) => {
    const ok = await copyText(text);
    if (ok) toast.success(`${label}已复制`);
    else toast.error('复制失败');
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-end">
        <Button onClick={() => setCreateOpen(true)} size="sm">
          <Plus className="h-4 w-4" />
          创建密钥
        </Button>
      </div>

      {/* Desktop table */}
      <div className="hidden md:block">
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>密钥</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead>最近使用</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(keys ?? []).map((k: ApiKey) => (
                <TableRow key={k.id}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell className="font-mono">{k.keyPrefix}…{k.keySuffix}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {formatTime(k.createdAt)}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {k.lastUsedAt ? formatTime(k.lastUsedAt) : '—'}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="iconSm"
                        onClick={() => copyToClipboard(`${k.keyPrefix}…${k.keySuffix}`, '密钥')}
                        title="复制密钥"
                      >
                        <Copy className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="iconSm"
                        onClick={() => setRotateTarget(k)}
                        title="轮换密钥"
                      >
                        <RotateCw className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="iconSm"
                        onClick={() => setRevokeTarget(k)}
                        title="撤销密钥"
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {(!keys || keys.length === 0) && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                    暂无密钥
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {(keys ?? []).map((k: ApiKey) => (
          <Card key={k.id}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-medium text-sm">{k.name}</span>
                <Badge variant="success">活跃</Badge>
              </div>
              <p className="font-mono text-xs text-muted-foreground">{k.keyPrefix}…{k.keySuffix}</p>
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>创建：{formatTime(k.createdAt)}</span>
                <span>使用：{k.lastUsedAt ? formatTime(k.lastUsedAt) : '—'}</span>
              </div>
              <div className="flex gap-2 pt-1">
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-1"
                  onClick={() => copyToClipboard(`${k.keyPrefix}…${k.keySuffix}`, '密钥')}
                >
                  <Copy className="h-3.5 w-3.5" />
                  复制
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-1"
                  onClick={() => setRotateTarget(k)}
                >
                  <RotateCw className="h-3.5 w-3.5" />
                  轮换
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-1 text-destructive hover:text-destructive"
                  onClick={() => setRevokeTarget(k)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                  撤销
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
        {(!keys || keys.length === 0) && (
          <p className="text-center text-muted-foreground py-8 text-sm">暂无密钥</p>
        )}
      </div>

      {/* Create dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogHeader>
          <DialogTitle>创建密钥</DialogTitle>
          <DialogDescription>为新密钥指定一个名称以便识别。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div>
            <Label htmlFor="key-name">名称</Label>
            <Input
              id="key-name"
              value={keyName}
              onChange={(e) => setKeyName(e.target.value)}
              placeholder="例如：生产环境"
              className="mt-1.5"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setCreateOpen(false)}>
            取消
          </Button>
          <Button
            onClick={() => createMutation.mutate(keyName)}
            disabled={createMutation.isPending || !keyName.trim()}
          >
            创建
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Reveal key dialog */}
      <Dialog open={!!revealKey} onOpenChange={(open) => !open && setRevealKey(null)}>
        <DialogHeader>
          <DialogTitle>密钥已创建</DialogTitle>
          <DialogDescription>
            请立即复制密钥，关闭后将无法再次查看。
          </DialogDescription>
        </DialogHeader>
        <div className="rounded-md border bg-muted/50 p-3 mt-2">
          <code className="break-all text-xs font-mono">{revealKey?.secret ?? ''}</code>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => setRevealKey(null)}
          >
            关闭
          </Button>
          <Button
            onClick={() => {
              if (revealKey?.secret) {
                copyToClipboard(revealKey.secret, '完整密钥');
              }
            }}
          >
            <Copy className="h-4 w-4" />
            复制密钥
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Revoke confirmation dialog */}
      <Dialog
        open={!!revokeTarget}
        onOpenChange={(open) => !open && setRevokeTarget(null)}
      >
        <DialogHeader>
          <DialogTitle>撤销密钥</DialogTitle>
          <DialogDescription>
            确定要撤销密钥「{revokeTarget?.name}」吗？此操作不可恢复。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setRevokeTarget(null)}>
            取消
          </Button>
          <Button
            variant="destructive"
            onClick={() => revokeTarget && revokeMutation.mutate(revokeTarget.id)}
            disabled={revokeMutation.isPending}
          >
            确认撤销
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Rotate confirmation dialog */}
      <Dialog
        open={!!rotateTarget}
        onOpenChange={(open) => !open && setRotateTarget(null)}
      >
        <DialogHeader>
          <DialogTitle>轮换密钥</DialogTitle>
          <DialogDescription>
            确定要轮换密钥「{rotateTarget?.name}」吗？将生成新密钥，旧密钥立即失效。新密钥仅显示一次。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setRotateTarget(null)}>
            取消
          </Button>
          <Button
            onClick={() => rotateTarget && rotateMutation.mutate(rotateTarget.id)}
            disabled={rotateMutation.isPending}
          >
            确认轮换
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}

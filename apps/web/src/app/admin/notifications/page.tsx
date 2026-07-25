'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminNotificationApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Checkbox } from '@/components/ui/checkbox';
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
import { Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminNotificationInput } from '@/types/admin';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function formatRecipient(userId: string) {
  if (userId === 'all') return <Badge variant="secondary">全体</Badge>;
  if (userId.length > 8) return userId.slice(0, 8) + '…';
  return userId;
}

export default function AdminNotificationsPage() {
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ['admin', 'notifications'],
    queryFn: adminNotificationApi.list,
  });

  const notifications = data ?? [];

  // ---- Send mutation ----
  const sendMutation = useMutation({
    mutationFn: (input: AdminNotificationInput) => adminNotificationApi.send(input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'notifications'] });
      toast.success('通知已发送');
    },
    onError: () => toast.error('发送失败'),
  });

  // ---- Delete mutation ----
  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminNotificationApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'notifications'] });
    },
    onError: () => toast.error('删除失败'),
  });

  // ---- Send dialog state ----
  const [sendOpen, setSendOpen] = useState(false);
  const [sendAll, setSendAll] = useState(true);
  const [form, setForm] = useState({
    userId: '',
    type: '',
    title: '',
    body: '',
    actionType: '',
    actionLabel: '',
    actionHref: '',
  });

  function resetForm() {
    setSendAll(true);
    setForm({ userId: '', type: '', title: '', body: '', actionType: '', actionLabel: '', actionHref: '' });
  }

  function handleSendOpenChange(open: boolean) {
    setSendOpen(open);
    if (!open) resetForm();
  }

  function handleSend() {
    const action =
      form.actionType || form.actionLabel || form.actionHref
        ? { type: form.actionType || 'link', label: form.actionLabel, ...(form.actionHref ? { href: form.actionHref } : {}) }
        : null;
    const input: AdminNotificationInput = {
      userId: sendAll ? '' : form.userId,
      type: form.type || 'info',
      title: form.title,
      body: form.body,
      action,
    };
    sendMutation.mutate(input, {
      onSuccess: () => {
        setSendOpen(false);
        resetForm();
      },
    });
  }

  // ---- Delete confirm ----
  const [confirm, setConfirm] = useState<{
    open: boolean;
    id: string;
  }>({ open: false, id: '' });

  function handleDelete(id: string) {
    setConfirm({ open: true, id });
  }

  function confirmDelete() {
    deleteMutation.mutate(confirm.id, {
      onSettled: () => setConfirm({ open: false, id: '' }),
    });
  }

  return (
    <div className="space-y-6">
      {/* Top bar */}
      <div className="flex justify-between">
        <Button onClick={() => setSendOpen(true)}>
          <Plus className="mr-1 h-4 w-4" />
          发送通知
        </Button>
      </div>

      {/* Desktop table */}
      <div className="hidden md:block">
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>接收者</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>标题</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>已读</TableHead>
                <TableHead>发送时间</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {notifications.map((n) => (
                <TableRow key={n.id}>
                  <TableCell>{formatRecipient(n.userId)}</TableCell>
                  <TableCell>
                    <Badge variant="default">{n.type}</Badge>
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate">{n.title}</TableCell>
                  <TableCell>
                    {n.action ? (
                      <span className="text-primary underline underline-offset-2 text-sm">
                        {n.action.label}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {n.readAt ? (
                      <span className="text-xs text-muted-foreground">{formatTime(n.readAt)}</span>
                    ) : (
                      <Badge variant="warning">未读</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(n.createdAt)}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="text-destructive hover:text-destructive"
                      onClick={() => handleDelete(n.id)}
                      disabled={deleteMutation.isPending}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {notifications.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    暂无通知
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {notifications.map((n) => (
          <Card key={n.id}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-medium text-sm truncate mr-2">{n.title}</span>
                <Badge variant="default">{n.type}</Badge>
              </div>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>{n.userId === 'all' ? '全体' : n.userId}</span>
                <span>·</span>
                {n.readAt ? (
                  <span>已读 {formatTime(n.readAt)}</span>
                ) : (
                  <Badge variant="warning" className="text-[10px] px-1 py-0">未读</Badge>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">{formatTime(n.createdAt)}</span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 text-destructive hover:text-destructive"
                  onClick={() => handleDelete(n.id)}
                  disabled={deleteMutation.isPending}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
        {notifications.length === 0 && (
          <p className="text-center text-muted-foreground py-8 text-sm">暂无通知</p>
        )}
      </div>

      {/* Send dialog */}
      <Dialog open={sendOpen} onOpenChange={handleSendOpenChange}>
        <DialogHeader>
          <DialogTitle>发送通知</DialogTitle>
          <DialogDescription>向指定用户或全体用户发送通知</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {/* Recipient */}
          <div className="flex items-center gap-2">
            <Checkbox
              checked={sendAll}
              onClick={() => setSendAll((v) => !v)}
              id="send-all"
            />
            <label htmlFor="send-all" className="text-sm cursor-pointer">
              发送给全体用户
            </label>
          </div>
          {!sendAll && (
            <Input
              placeholder="用户 ID"
              value={form.userId}
              onChange={(e) => setForm((f) => ({ ...f, userId: e.target.value }))}
            />
          )}
          {/* Type */}
          <Input
            placeholder="类型（info/success/warning，选填）"
            value={form.type}
            onChange={(e) => setForm((f) => ({ ...f, type: e.target.value }))}
          />
          {/* Title */}
          <Input
            placeholder="标题（必填）"
            value={form.title}
            onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
          />
          {/* Body */}
          <Textarea
            rows={4}
            placeholder="通知内容"
            value={form.body}
            onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
          />
          {/* Action (optional) */}
          <div className="space-y-2">
            <p className="text-sm text-muted-foreground">Action（选填，全部留空则无 Action）</p>
            <div className="grid grid-cols-3 gap-2">
              <Input
                placeholder="type (link)"
                value={form.actionType}
                onChange={(e) => setForm((f) => ({ ...f, actionType: e.target.value }))}
              />
              <Input
                placeholder="按钮文本"
                value={form.actionLabel}
                onChange={(e) => setForm((f) => ({ ...f, actionLabel: e.target.value }))}
              />
              <Input
                placeholder="/panel/xxx"
                value={form.actionHref}
                onChange={(e) => setForm((f) => ({ ...f, actionHref: e.target.value }))}
              />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleSendOpenChange(false)} disabled={sendMutation.isPending}>
            取消
          </Button>
          <Button onClick={handleSend} disabled={sendMutation.isPending || !form.title}>
            {sendMutation.isPending ? '发送中…' : '发送'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Delete confirm */}
      <ConfirmDialog
        open={confirm.open}
        onOpenChange={(open) => setConfirm((c) => ({ ...c, open }))}
        title="删除通知"
        description="确定要删除该通知吗？此操作不可撤销。"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}

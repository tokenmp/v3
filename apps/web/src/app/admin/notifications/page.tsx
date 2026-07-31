'use client';

import { useMemo, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminApi, adminNotificationApi } from '@/lib/api/admin';
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
import { Plus, Trash2, UserRound, X } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminNotificationInput, AdminUser } from '@/types/admin';
import { PageHeader } from '@/components/page-header';

const BROADCAST_USER_ID = '00000000-0000-0000-0000-000000000000';

const TYPE_OPTIONS = [
  { value: 'info', label: '普通信息' },
  { value: 'system', label: '系统通知' },
  { value: 'success', label: '成功提示' },
  { value: 'warning', label: '风险/警告' },
  { value: 'maintenance', label: '维护公告' },
  { value: 'plan_activated', label: '套餐已启用' },
  { value: 'plan_disabled', label: '套餐已停用' },
  { value: 'api_key', label: 'API Key 相关' },
  { value: 'billing', label: '计费相关' },
  { value: 'custom', label: '自定义类型' },
] as const;

const ACTION_TARGETS = [
  { value: 'none', label: '无 Action', href: '', defaultLabel: '' },
  { value: 'panel-home', label: '用户面板首页', href: '/panel', defaultLabel: '查看面板' },
  { value: 'panel-keys', label: 'API 密钥', href: '/panel/keys', defaultLabel: '管理 API 密钥' },
  { value: 'panel-requests', label: '请求日志', href: '/panel/requests', defaultLabel: '查看请求日志' },
  { value: 'panel-models', label: '模型列表', href: '/panel/models', defaultLabel: '查看模型' },
  { value: 'panel-auto-model', label: 'Auto 模型', href: '/panel/auto-model', defaultLabel: '配置 Auto 模型' },
  { value: 'panel-announcements', label: '公告', href: '/panel/announcements', defaultLabel: '查看公告' },
  { value: 'panel-notifications', label: '通知中心', href: '/panel/notifications', defaultLabel: '查看通知' },
  { value: 'panel-changelogs', label: '版本日志', href: '/panel/changelogs', defaultLabel: '查看版本日志' },
  { value: 'panel-settings', label: '个人设置', href: '/panel/settings', defaultLabel: '前往设置' },
  { value: 'custom', label: '自定义链接', href: '', defaultLabel: '' },
] as const;

type ActionTargetValue = typeof ACTION_TARGETS[number]['value'];

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function isBroadcast(userId: string) {
  return userId === '' || userId === 'all' || userId === BROADCAST_USER_ID;
}

function formatRecipient(userId: string) {
  if (isBroadcast(userId)) return <Badge variant="secondary">全体用户</Badge>;
  if (userId.length > 8) return userId.slice(0, 8) + '…';
  return userId;
}

function labelForType(value: string) {
  return TYPE_OPTIONS.find((o) => o.value === value)?.label ?? value;
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
    onError: (e: unknown) => toast.error('发送失败', { description: e instanceof Error ? e.message : undefined }),
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
  const [userSearch, setUserSearch] = useState('');
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);
  const [typePreset, setTypePreset] = useState<string>('info');
  const [actionTarget, setActionTarget] = useState<ActionTargetValue>('none');
  const [form, setForm] = useState({
    customType: '',
    title: '',
    body: '',
    actionLabel: '',
    actionHref: '',
  });

  const trimmedUserSearch = userSearch.trim();
  const { data: userResults, isFetching: usersLoading } = useQuery({
    queryKey: ['admin', 'notification-user-search', trimmedUserSearch] as const,
    queryFn: () => adminApi.listUsers(1, 8, trimmedUserSearch, '', ''),
    enabled: sendOpen && !sendAll && selectedUser == null && trimmedUserSearch.length >= 2,
    staleTime: 30_000,
  });

  const selectedTarget = useMemo(
    () => ACTION_TARGETS.find((target) => target.value === actionTarget) ?? ACTION_TARGETS[0],
    [actionTarget],
  );

  const effectiveType = typePreset === 'custom' ? form.customType.trim() : typePreset;
  const effectiveActionHref = actionTarget === 'custom' ? form.actionHref.trim() : selectedTarget.href;
  const effectiveActionLabel = form.actionLabel.trim();

  function resetForm() {
    setSendAll(true);
    setUserSearch('');
    setSelectedUser(null);
    setTypePreset('info');
    setActionTarget('none');
    setForm({ customType: '', title: '', body: '', actionLabel: '', actionHref: '' });
  }

  function handleSendOpenChange(open: boolean) {
    setSendOpen(open);
    if (!open) resetForm();
  }

  function handleTargetChange(value: ActionTargetValue) {
    const target = ACTION_TARGETS.find((item) => item.value === value) ?? ACTION_TARGETS[0];
    setActionTarget(value);
    setForm((f) => ({
      ...f,
      actionLabel: target.defaultLabel,
      actionHref: target.href,
    }));
  }

  function validateForm(): string | null {
    if (!form.title.trim()) return '请填写通知标题';
    if (!form.body.trim()) return '请填写通知内容';
    if (!effectiveType) return '请选择或填写通知类型';
    if (!sendAll && !selectedUser) return '请选择一个接收用户';
    if (actionTarget !== 'none') {
      if (!effectiveActionLabel) return '请填写 Action 按钮文本';
      if (!effectiveActionHref) return '请选择或填写 Action 跳转地址';
      if (!effectiveActionHref.startsWith('/panel')) return 'Action 跳转地址必须是 /panel 开头的站内路径';
    }
    return null;
  }

  function handleSend() {
    const error = validateForm();
    if (error) {
      toast.error(error);
      return;
    }
    const action = actionTarget === 'none'
      ? null
      : { type: 'link', label: effectiveActionLabel, href: effectiveActionHref };
    const input: AdminNotificationInput = {
      userId: sendAll ? '' : selectedUser?.id,
      type: effectiveType,
      title: form.title.trim(),
      body: form.body.trim(),
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

  const sendDisabled = sendMutation.isPending || validateForm() != null;

  return (
    <div className="space-y-6">
      <PageHeader title="通知管理" description="发送与管理通知" actions={
        <Button onClick={() => setSendOpen(true)}>
          <Plus className="mr-1 h-4 w-4" />
          发送通知
        </Button>
      } />

      {/* Desktop table */}
      <div className="hidden md:block">
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table>
            <TableHeader className="bg-muted/30">
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
                    <Badge variant="default">{labelForType(n.type)}</Badge>
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
                      <span className="text-muted-foreground">{formatTime(n.readAt)}</span>
                    ) : (
                      <Badge variant="warning">未读</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {formatTime(n.createdAt)}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="iconSm"
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
        </div>
      </div>

      {/* Mobile card list */}
      <div className="md:hidden space-y-3">
        {notifications.map((n) => (
          <Card key={n.id}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-medium text-sm truncate mr-2">{n.title}</span>
                <Badge variant="default">{labelForType(n.type)}</Badge>
              </div>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span>{isBroadcast(n.userId) ? '全体用户' : n.userId}</span>
                <span>·</span>
                {n.readAt ? (
                  <span>已读 {formatTime(n.readAt)}</span>
                ) : (
                  <Badge variant="warning" className="text-[10px] px-1 py-0">未读</Badge>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">{formatTime(n.createdAt)}</span>
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
        <div className="space-y-5">
          {/* Recipient */}
          <section className="space-y-3 rounded-lg border p-3">
            <div className="text-sm font-medium">接收对象</div>
            <div className="flex items-center gap-2">
              <Checkbox
                checked={sendAll}
                onClick={() => {
                  setSendAll((v) => !v);
                  setSelectedUser(null);
                  setUserSearch('');
                }}
                id="send-all"
              />
              <label htmlFor="send-all" className="text-sm cursor-pointer">
                发送给全体用户
              </label>
            </div>
            {!sendAll && (
              <div className="space-y-2">
                {selectedUser ? (
                  <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-2 text-sm">
                    <UserRound className="h-4 w-4 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">{selectedUser.email}</div>
                      <div className="text-muted-foreground">{selectedUser.role === 'admin' ? '管理员' : '用户'} · {selectedUser.status === 'active' ? '正常' : '已禁用'}</div>
                    </div>
                    <button
                      type="button"
                      className="rounded p-1 text-muted-foreground hover:text-foreground"
                      onClick={() => setSelectedUser(null)}
                      aria-label="取消选择用户"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                ) : (
                  <>
                    <Input
                      placeholder="输入邮箱搜索用户"
                      value={userSearch}
                      onChange={(e) => setUserSearch(e.target.value)}
                    />
                    <div className="rounded-md border">
                      {trimmedUserSearch.length < 2 ? (
                        <div className="p-3 text-xs text-muted-foreground">至少输入 2 个字符开始搜索</div>
                      ) : usersLoading ? (
                        <div className="p-3 text-xs text-muted-foreground">搜索中…</div>
                      ) : (userResults?.users.length ?? 0) > 0 ? (
                        <div className="divide-y">
                          {userResults!.users.map((user) => (
                            <button
                              key={user.id}
                              type="button"
                              className="flex w-full items-center gap-2 p-3 text-left text-sm hover:bg-accent"
                              onClick={() => setSelectedUser(user)}
                            >
                              <UserRound className="h-4 w-4 text-muted-foreground" />
                              <span className="min-w-0 flex-1 truncate">{user.email}</span>
                              <Badge variant={user.status === 'active' ? 'success' : 'secondary'}>
                                {user.status === 'active' ? '正常' : '已禁用'}
                              </Badge>
                            </button>
                          ))}
                        </div>
                      ) : (
                        <div className="p-3 text-xs text-muted-foreground">未找到匹配用户</div>
                      )}
                    </div>
                  </>
                )}
              </div>
            )}
          </section>

          {/* Content */}
          <section className="space-y-3 rounded-lg border p-3">
            <div className="text-sm font-medium">通知内容</div>
            <div className="grid gap-2 sm:grid-cols-2">
              <select
                value={typePreset}
                onChange={(e) => setTypePreset(e.target.value)}
                className="h-10 rounded-md border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {TYPE_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
              {typePreset === 'custom' && (
                <Input
                  placeholder="自定义类型，如 plan_expiring"
                  value={form.customType}
                  onChange={(e) => setForm((f) => ({ ...f, customType: e.target.value }))}
                />
              )}
            </div>
            <Input
              placeholder="标题（必填）"
              value={form.title}
              onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
            />
            <Textarea
              rows={4}
              placeholder="通知内容（必填）"
              value={form.body}
              onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
            />
          </section>

          {/* Action (optional) */}
          <section className="space-y-3 rounded-lg border p-3">
            <div>
              <div className="text-sm font-medium">Action</div>
              <p className="text-muted-foreground">当前仅支持 Link 类型；未知类型客户端会忽略。</p>
            </div>
            <select
              value={actionTarget}
              onChange={(e) => handleTargetChange(e.target.value as ActionTargetValue)}
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {ACTION_TARGETS.map((target) => (
                <option key={target.value} value={target.value}>{target.label}</option>
              ))}
            </select>
            {actionTarget !== 'none' && (
              <div className="grid gap-2 sm:grid-cols-2">
                <Input
                  placeholder="按钮文本"
                  value={form.actionLabel}
                  onChange={(e) => setForm((f) => ({ ...f, actionLabel: e.target.value }))}
                />
                <Input
                  placeholder="/panel/xxx"
                  value={effectiveActionHref}
                  disabled={actionTarget !== 'custom'}
                  onChange={(e) => setForm((f) => ({ ...f, actionHref: e.target.value }))}
                />
              </div>
            )}
          </section>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleSendOpenChange(false)} disabled={sendMutation.isPending}>
            取消
          </Button>
          <Button onClick={handleSend} disabled={sendDisabled}>
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

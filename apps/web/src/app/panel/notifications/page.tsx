'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { noticeApi } from '@/lib/api/notice';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Inbox, CheckCheck } from 'lucide-react';
import { NotificationAction } from '@/components/notification-action';
import { toast } from 'sonner';
import type { Notification } from '@/types';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function NotificationRow({
  item,
  onMarkRead,
}: {
  item: Notification;
  onMarkRead: (id: string) => void;
}) {
  const unread = item.read_at === null;

  return (
    <TableRow
      className={unread ? 'cursor-pointer focus-inset' : 'focus-inset'}
      onClick={() => unread && onMarkRead(item.id)}
    >
      <TableCell className="w-3">
        {unread && <span className="inline-block h-2 w-2 rounded-full bg-primary" />}
      </TableCell>
      <TableCell>
        <div className={unread ? 'font-medium' : ''}>{item.title}</div>
      </TableCell>
      <TableCell className="text-sm text-muted-foreground max-w-xs truncate hidden md:table-cell">
        {item.body}
      </TableCell>
      <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
        {formatTime(item.created_at)}
      </TableCell>
      <TableCell>
        <NotificationAction action={item.action} />
      </TableCell>
    </TableRow>
  );
}

function NotificationCard({
  item,
  onMarkRead,
}: {
  item: Notification;
  onMarkRead: (id: string) => void;
}) {
  const unread = item.read_at === null;

  return (
    <Card
      className={unread ? 'cursor-pointer focus-inset' : 'focus-inset'}
      onClick={() => unread && onMarkRead(item.id)}
    >
      <CardContent className="p-4 space-y-2">
        <div className="flex items-start gap-2">
          {unread && <span className="mt-1.5 inline-block h-2 w-2 shrink-0 rounded-full bg-primary" />}
          <div className="min-w-0 flex-1">
            <p className={unread ? 'font-medium text-sm' : 'text-sm'}>{item.title}</p>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">{item.body}</p>
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">{formatTime(item.created_at)}</span>
          <NotificationAction action={item.action} />
        </div>
      </CardContent>
    </Card>
  );
}

export default function NotificationsPage() {
  const queryClient = useQueryClient();

  const { data, isLoading } = useQuery({
    queryKey: ['notifications'],
    queryFn: () => noticeApi.listNotifications(),
  });

  const markReadMutation = useMutation({
    mutationFn: noticeApi.markRead,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notifications'] });
    },
  });

  const markAllReadMutation = useMutation({
    mutationFn: noticeApi.markAllRead,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notifications'] });
      toast.success('已全部标记为已读');
    },
  });

  const items = data?.items ?? [];
  const hasUnread = items.some((n) => n.read_at === null);

  const handleMarkRead = (id: string) => {
    markReadMutation.mutate(id);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">通知</h2>
        {hasUnread && (
          <Button
            size="sm"
            variant="outline"
            className="focus-inset"
            onClick={() => markAllReadMutation.mutate()}
            disabled={markAllReadMutation.isPending}
          >
            <CheckCheck className="h-4 w-4" />
            全部标记已读
          </Button>
        )}
      </div>

      {isLoading && (
        <Card>
          <CardContent className="flex items-center justify-center py-16 text-muted-foreground">
            加载中…
          </CardContent>
        </Card>
      )}

      {!isLoading && items.length === 0 && (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-16 text-center">
            <Inbox className="h-12 w-12 text-muted-foreground mb-4" />
            <p className="text-muted-foreground">暂无通知</p>
          </CardContent>
        </Card>
      )}

      {!isLoading && items.length > 0 && (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-3" />
                    <TableHead>标题</TableHead>
                    <TableHead>内容</TableHead>
                    <TableHead>时间</TableHead>
                    <TableHead>操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((n) => (
                    <NotificationRow key={n.id} item={n} onMarkRead={handleMarkRead} />
                  ))}
                </TableBody>
              </Table>
            </Card>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {items.map((n) => (
              <NotificationCard key={n.id} item={n} onMarkRead={handleMarkRead} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

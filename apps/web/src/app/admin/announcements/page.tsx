'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminAnnouncementApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
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
import type { AdminAnnouncement, AdminAnnouncementInput } from '@/types/admin';
import { PageHeader } from '@/components/page-header';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

const SEVERITY_OPTIONS: { value: AdminAnnouncementInput['severity']; label: string; badgeVariant: 'secondary' | 'warning' | 'destructive' }[] = [
  { value: 'info', label: '提醒', badgeVariant: 'secondary' },
  { value: 'warning', label: '警告', badgeVariant: 'warning' },
  { value: 'maintenance', label: '维护', badgeVariant: 'destructive' },
];

interface FormState {
  title: string;
  summary: string;
  body: string;
  severity: AdminAnnouncementInput['severity'];
  publishNow: boolean;
}

const emptyForm: FormState = {
  title: '',
  summary: '',
  body: '',
  severity: 'info',
  publishNow: false,
};

export default function AdminAnnouncementsPage() {
  const qc = useQueryClient();

  const { data: announcements = [], isLoading } = useQuery({
    queryKey: ['admin', 'announcements'],
    queryFn: adminAnnouncementApi.list,
  });

  const createMutation = useMutation({
    mutationFn: (input: AdminAnnouncementInput) => adminAnnouncementApi.create(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'announcements'] });
      toast.success('公告已创建');
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: AdminAnnouncementInput }) =>
      adminAnnouncementApi.update(id, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'announcements'] });
      toast.success('公告已更新');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminAnnouncementApi.delete(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'announcements'] });
      toast.success('公告已删除');
    },
  });

  // Dialog state
  const [formOpen, setFormOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<AdminAnnouncement | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);

  // Delete confirm
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  function openCreate() {
    setEditingItem(null);
    setForm(emptyForm);
    setFormOpen(true);
  }

  function openEdit(item: AdminAnnouncement) {
    setEditingItem(item);
    setForm({
      title: item.title,
      summary: item.summary,
      body: item.body,
      severity: item.severity,
      publishNow: item.publishedAt !== null,
    });
    setFormOpen(true);
  }

  function handleSubmit() {
    if (!form.title.trim()) {
      toast.error('请填写标题');
      return;
    }
    const input: AdminAnnouncementInput = {
      title: form.title.trim(),
      summary: form.summary.trim(),
      body: form.body,
      severity: form.severity,
      publishedAt: form.publishNow ? new Date().toISOString() : null,
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

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      <PageHeader title="公告管理" description="发布与管理公告" actions={
        <Button onClick={openCreate}>
          <Plus />
          新建公告
        </Button>
      } />

      {announcements.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无公告</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <div className="overflow-hidden rounded-lg border border-border bg-card">
            <Table>
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead>标题</TableHead>
                  <TableHead>级别</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>发布时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {announcements.map((a) => {
                  const sev = SEVERITY_OPTIONS.find((s) => s.value === a.severity);
                  return (
                    <TableRow key={a.id}>
                      <TableCell className="font-medium">{a.title}</TableCell>
                      <TableCell>
                        <Badge variant={sev?.badgeVariant ?? 'secondary'}>
                          {sev?.label ?? a.severity}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={a.publishedAt ? 'success' : 'secondary'}>
                          {a.publishedAt ? '已发布' : '草稿'}
                        </Badge>
                      </TableCell>
                      <TableCell>{a.publishedAt ? formatTime(a.publishedAt) : '—'}</TableCell>
                      <TableCell className="text-right">
                        <Button variant="ghost" size="iconSm" onClick={() => openEdit(a)}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="iconSm"
                          className="text-destructive"
                          onClick={() => openDelete(a.id)}
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
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {announcements.map((a) => {
              const sev = SEVERITY_OPTIONS.find((s) => s.value === a.severity);
              return (
                <Card key={a.id}>
                  <CardContent className="p-4 space-y-3">
                    <div className="flex items-start justify-between gap-2">
                      <span className="font-medium">{a.title}</span>
                      <div className="flex items-center gap-1.5 shrink-0">
                        <Badge variant={sev?.badgeVariant ?? 'secondary'}>
                          {sev?.label ?? a.severity}
                        </Badge>
                        <Badge variant={a.publishedAt ? 'success' : 'secondary'}>
                          {a.publishedAt ? '已发布' : '草稿'}
                        </Badge>
                      </div>
                    </div>
                    {a.publishedAt && (
                      <p className="text-xs text-muted-foreground">
                        {formatTime(a.publishedAt)}
                      </p>
                    )}
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="iconSm" onClick={() => openEdit(a)}>
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="iconSm"
                        className="text-destructive"
                        onClick={() => openDelete(a.id)}
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
          <DialogTitle>{editingItem ? '编辑公告' : '新建公告'}</DialogTitle>
          <DialogDescription>
            {editingItem ? '修改公告内容后保存。' : '填写公告信息并创建。'}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="ann-title">标题</Label>
            <Input
              id="ann-title"
              value={form.title}
              onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
              placeholder="公告标题"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="ann-summary">摘要</Label>
            <Input
              id="ann-summary"
              value={form.summary}
              onChange={(e) => setForm((f) => ({ ...f, summary: e.target.value }))}
              placeholder="简短摘要（选填）"
            />
          </div>
          <div className="space-y-2">
            <Label>级别</Label>
            <div className="flex gap-2">
              {SEVERITY_OPTIONS.map((opt) => (
                <Button
                  key={opt.value}
                  type="button"
                  variant={form.severity === opt.value ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => setForm((f) => ({ ...f, severity: opt.value }))}
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="ann-publish"
              checked={form.publishNow}
              onClick={() =>
                setForm((f) => ({ ...f, publishNow: !f.publishNow }))
              }
            />
            <Label htmlFor="ann-publish" className="cursor-pointer">
              立即发布
            </Label>
          </div>
          <div className="space-y-2">
            <Label htmlFor="ann-body">内容（Markdown）</Label>
            <Textarea
              id="ann-body"
              rows={6}
              value={form.body}
              onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
              placeholder="公告正文，支持 Markdown 语法"
            />
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
        title="删除公告"
        description="确定要删除此公告吗？此操作不可撤销。"
        confirmText="删除"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={handleDelete}
      />
    </div>
  );
}

'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { adminChangelogApi } from '@/lib/api/admin';
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
import { Markdown } from '@/components/markdown';
import { Plus, Pencil, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { AdminChangelog, AdminChangelogInput } from '@/types/admin';
import { PageHeader } from '@/components/page-header';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

interface FormState {
  version: string;
  title: string;
  body: string;
  publishNow: boolean;
}

const emptyForm: FormState = { version: '', title: '', body: '', publishNow: false };

function formToInput(f: FormState): AdminChangelogInput {
  return {
    version: f.version,
    title: f.title,
    body: f.body,
    publishedAt: f.publishNow ? new Date().toISOString() : null,
  };
}

function itemToForm(c: AdminChangelog): FormState {
  return {
    version: c.version,
    title: c.title,
    body: c.body,
    publishNow: c.publishedAt !== null,
  };
}

export default function AdminChangelogsPage() {
  const queryClient = useQueryClient();

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'changelogs'],
    queryFn: adminChangelogApi.list,
  });

  const changelogs = data ?? [];

  // ---- Mutations ----
  const createMutation = useMutation({
    mutationFn: (input: AdminChangelogInput) => adminChangelogApi.create(input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'changelogs'] });
      toast.success('版本日志已创建');
    },
    onError: () => toast.error('创建失败'),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: AdminChangelogInput }) =>
      adminChangelogApi.update(id, input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'changelogs'] });
      toast.success('版本日志已更新');
    },
    onError: () => toast.error('更新失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminChangelogApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'changelogs'] });
      toast.success('版本日志已删除');
    },
    onError: () => toast.error('删除失败'),
  });

  // ---- Dialog state ----
  const [formOpen, setFormOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);

  const [deleteTarget, setDeleteTarget] = useState<AdminChangelog | null>(null);

  function openCreate() {
    setEditingId(null);
    setForm(emptyForm);
    setFormOpen(true);
  }

  function openEdit(c: AdminChangelog) {
    setEditingId(c.id);
    setForm(itemToForm(c));
    setFormOpen(true);
  }

  function handleSubmit() {
    if (!form.version.trim() || !form.title.trim()) {
      toast.error('请填写版本号和标题');
      return;
    }
    const input = formToInput(form);
    if (editingId) {
      updateMutation.mutate({ id: editingId, input }, { onSuccess: () => setFormOpen(false) });
    } else {
      createMutation.mutate(input, { onSuccess: () => setFormOpen(false) });
    }
  }

  function confirmDelete() {
    if (!deleteTarget) return;
    deleteMutation.mutate(deleteTarget.id, {
      onSuccess: () => setDeleteTarget(null),
    });
  }

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="space-y-6">
      <PageHeader title="版本日志" description="发布与管理版本日志" actions={
        <Button onClick={openCreate}>
          <Plus className="mr-2 h-4 w-4" />
          新建版本
        </Button>
      } />

      {/* Loading */}
      {isLoading && (
        <p className="text-center text-muted-foreground py-8 text-sm">加载中…</p>
      )}

      {!isLoading && changelogs.length === 0 && (
        <p className="text-center text-muted-foreground py-8 text-sm">暂无版本日志</p>
      )}

      {!isLoading && changelogs.length > 0 && (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <Table>
                <TableHeader className="bg-muted/30">
                  <TableRow>
                    <TableHead>版本号</TableHead>
                    <TableHead>标题</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>发布时间</TableHead>
                    <TableHead>操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {changelogs.map((c) => (
                    <TableRow key={c.id}>
                      <TableCell>
                        <Badge variant="default" className="font-mono">
                          {c.version}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-medium text-sm">{c.title}</TableCell>
                      <TableCell>
                        {c.publishedAt !== null ? (
                          <Badge variant="success">已发布</Badge>
                        ) : (
                          <Badge variant="secondary">草稿</Badge>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {c.publishedAt ? formatTime(c.publishedAt) : '—'}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1">
                          <Button variant="ghost" size="sm" onClick={() => openEdit(c)}>
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            onClick={() => setDeleteTarget(c)}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {changelogs.map((c) => (
              <Card key={c.id}>
                <CardContent className="p-4 space-y-3">
                  <div className="flex items-center justify-between">
                    <Badge variant="default" className="font-mono">
                      {c.version}
                    </Badge>
                    {c.publishedAt !== null ? (
                      <Badge variant="success">已发布</Badge>
                    ) : (
                      <Badge variant="secondary">草稿</Badge>
                    )}
                  </div>
                  <p className="font-medium text-sm">{c.title}</p>
                  <p className="text-xs text-muted-foreground">
                    {c.publishedAt ? formatTime(c.publishedAt) : '—'}
                  </p>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="sm" onClick={() => openEdit(c)}>
                      <Pencil className="h-4 w-4 mr-1" />
                      编辑
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(c)}
                    >
                      <Trash2 className="h-4 w-4 mr-1" />
                      删除
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </>
      )}

      {/* Create / Edit dialog */}
      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogHeader>
          <DialogTitle>{editingId ? '编辑版本日志' : '新建版本日志'}</DialogTitle>
          <DialogDescription>
            {editingId ? '修改版本日志内容' : '填写新版本日志信息'}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="cl-version">版本号</Label>
            <Input
              id="cl-version"
              placeholder="v3.1.0"
              value={form.version}
              onChange={(e) => setForm((f) => ({ ...f, version: e.target.value }))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cl-title">标题</Label>
            <Input
              id="cl-title"
              placeholder="版本标题"
              value={form.title}
              onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
            />
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="cl-publish"
              checked={form.publishNow}
              onCheckedChange={(checked) =>
                setForm((f) => ({ ...f, publishNow: checked === true }))
              }
            />
            <Label htmlFor="cl-publish" className="cursor-pointer">
              立即发布
            </Label>
          </div>
          <div className="space-y-2">
            <Label htmlFor="cl-body">内容（Markdown）</Label>
            <Textarea
              id="cl-body"
              rows={8}
              placeholder="## 新功能&#10;&#10;- 功能 A&#10;- 功能 B"
              value={form.body}
              onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
            />
          </div>
          {form.body.trim() && (
            <div className="space-y-1">
              <Label>预览</Label>
              <div className="rounded-md border p-3 max-h-64 overflow-y-auto">
                <Markdown>{form.body}</Markdown>
              </div>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setFormOpen(false)} disabled={isSubmitting}>
            取消
          </Button>
          <Button onClick={handleSubmit} disabled={isSubmitting}>
            {isSubmitting ? '提交中…' : editingId ? '保存' : '创建'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Delete confirm dialog */}
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title="删除版本日志"
        description={`确定要删除版本 ${deleteTarget?.version ?? ''} 的日志吗？此操作不可撤销。`}
        destructive
        loading={deleteMutation.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}

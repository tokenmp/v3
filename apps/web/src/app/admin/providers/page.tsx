'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import type { AdminProvider } from '@/types/admin';
import { FilterChip } from '@/components/filter-chip';
import { Modal } from '@/components/ui/modal';
import {
  Field,
  FormActions,
  FormSection,
  SelectField,
  SwitchField,
  TabField,
  TextField,
} from '@/components/ui/field';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';

const PROTOCOL_OPTIONS = [
  { value: 'openai_chat', label: 'openai_chat' },
  { value: 'anthropic_messages', label: 'anthropic_messages' },
  { value: 'openai_responses', label: 'openai_responses' },
  { value: 'openai_images', label: 'openai_images' },
];

const SDK_TAB_OPTIONS = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
];

type StatusFilter = 'all' | 'active' | 'disabled';

type ProviderDraft = {
  id: string;
  name: string;
  displayLabel: string;
  baseURL: string;
  sdkKind: AdminProvider['sdkKind'];
  protocol: string;
  status: AdminProvider['status'];
};

function emptyDraft(): ProviderDraft {
  return {
    id: '',
    name: '',
    displayLabel: '',
    baseURL: '',
    sdkKind: 'openai',
    protocol: 'openai_chat',
    status: 'active',
  };
}

export default function AdminProvidersPage() {
  const queryClient = useQueryClient();
  const { data: providers = [], isLoading } = useQuery({
    queryKey: ['admin', 'providers'],
    queryFn: adminConfigApi.listProviders,
  });

  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<StatusFilter>('all');
  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<AdminProvider | null>(null);

  const filtered = useMemo(() => {
    const kw = search.trim().toLowerCase();
    return providers.filter((p) => {
      const matchKw =
        !kw ||
        p.name.toLowerCase().includes(kw) ||
        p.displayLabel.toLowerCase().includes(kw);
      const matchStatus =
        statusF === 'all' ||
        (statusF === 'active' && p.status === 'active') ||
        (statusF === 'disabled' && p.status === 'disabled');
      return matchKw && matchStatus;
    });
  }, [providers, search, statusF]);

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminConfigApi.deleteProvider(id),
    onSuccess: () => {
      toast.success('已删除 Provider');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'providers'] });
    },
    onError: (e: unknown) => {
      toast.error('删除失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const handleDelete = (p: AdminProvider) => {
    if (!confirm(`删除「${p.name}」？若有凭据/路由会失败`)) return;
    deleteMutation.mutate(p.id);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">Provider 管理</h1>

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          placeholder="搜索 name / 显示名"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-[var(--control-height-sm)] min-w-56 flex-1 rounded-sm border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        <div className="flex flex-wrap gap-1.5 text-xs">
          <FilterChip label="全部" active={statusF === 'all'} onClick={() => setStatusF('all')} />
          <FilterChip
            label="启用"
            active={statusF === 'active'}
            onClick={() => setStatusF('active')}
          />
          <FilterChip
            label="停用"
            active={statusF === 'disabled'}
            onClick={() => setStatusF('disabled')}
          />
        </div>
        <button
          type="button"
          onClick={() => setCreateOpen(true)}
          className="inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          <Plus className="size-3.5" />
          新建 Provider
        </button>
      </div>

      {/* Table */}
      <div className="rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead>名称</TableHead>
                <TableHead>显示名</TableHead>
                <TableHead>Base URL</TableHead>
                <TableHead>SDK</TableHead>
                <TableHead>协议</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading ? (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                    加载中…
                  </TableCell>
                </TableRow>
              ) : filtered.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                    暂无 Provider
                  </TableCell>
                </TableRow>
              ) : (
                filtered.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="font-mono text-xs">{p.name}</TableCell>
                    <TableCell className="text-sm">{p.displayLabel || '-'}</TableCell>
                    <TableCell
                      className="max-w-[220px] truncate font-mono text-[10px] text-muted-foreground"
                      title={p.baseURL}
                    >
                      {p.baseURL}
                    </TableCell>
                    <TableCell className="text-xs">{p.sdkKind}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{p.protocol}</TableCell>
                    <TableCell>
                      <StatusBadge status={p.status} />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <button
                          type="button"
                          onClick={() => setEditItem(p)}
                          className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                          aria-label="编辑"
                          title="编辑"
                        >
                          <Pencil className="size-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => handleDelete(p)}
                          className="rounded-sm p-1.5 text-muted-foreground hover:bg-red-100 hover:text-red-700"
                          aria-label="删除"
                          title="删除"
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {createOpen ? (
        <ProviderFormModal
          mode="create"
          onClose={() => setCreateOpen(false)}
          onSaved={() => setCreateOpen(false)}
        />
      ) : null}
      {editItem ? (
        <ProviderFormModal
          mode="edit"
          item={editItem}
          onClose={() => setEditItem(null)}
          onSaved={() => setEditItem(null)}
        />
      ) : null}
    </div>
  );
}

function StatusBadge({ status }: { status: AdminProvider['status'] }) {
  const isActive = status === 'active';
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium',
        isActive ? 'bg-green-100 text-green-700' : 'bg-muted text-muted-foreground',
      )}
    >
      {isActive ? '启用' : status === 'disabled' ? '停用' : status}
    </span>
  );
}

function ProviderFormModal({
  mode,
  item,
  onClose,
  onSaved,
}: {
  mode: 'create' | 'edit';
  item?: AdminProvider;
  onClose: () => void;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<ProviderDraft>(
    item
      ? {
          id: item.id,
          name: item.name,
          displayLabel: item.displayLabel,
          baseURL: item.baseURL,
          sdkKind: item.sdkKind,
          protocol: item.protocol,
          status: item.status,
        }
      : emptyDraft(),
  );

  const patch = (p: Partial<ProviderDraft>) => setDraft((d) => ({ ...d, ...p }));

  const createMutation = useMutation({
    mutationFn: (input: ProviderDraft) =>
      adminConfigApi.createProvider({
        id: input.id,
        name: input.name,
        baseURL: input.baseURL,
        sdkKind: input.sdkKind,
        protocol: input.protocol,
        displayLabel: input.displayLabel || undefined,
        selector: input.id,
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已创建 Provider');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'providers'] });
      onSaved();
    },
    onError: (e: unknown) => {
      toast.error('创建失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (input: ProviderDraft) =>
      adminConfigApi.updateProvider(item!.id, {
        name: input.name,
        displayLabel: input.displayLabel,
        baseURL: input.baseURL,
        sdkKind: input.sdkKind,
        protocol: input.protocol,
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已更新 Provider');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'providers'] });
      onSaved();
    },
    onError: (e: unknown) => {
      toast.error('更新失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const submitting = createMutation.isPending || updateMutation.isPending;
  const canSubmit =
    draft.name.trim().length > 0 &&
    draft.baseURL.trim().length > 0 &&
    (mode === 'edit' || draft.id.trim().length > 0);

  const submit = () => {
    if (!canSubmit) return;
    if (mode === 'create') createMutation.mutate(draft);
    else updateMutation.mutate(draft);
  };

  return (
    <Modal
      open
      title={mode === 'edit' ? `编辑 ${item?.name ?? ''}` : '新建 Provider'}
      onClose={onClose}
      maxWidth="md"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={submit}
          submitting={submitting}
          submitLabel={mode === 'edit' ? '保存' : '创建'}
          disabled={!canSubmit}
        />
      }
    >
      <FormSection cols={2}>
        {mode === 'create' ? (
          <Field label="ID" required hint="唯一标识，如 deepseek（创建后不可改）">
            <TextField
              value={draft.id}
              onChange={(v) => patch({ id: v })}
              placeholder="deepseek"
            />
          </Field>
        ) : null}
        <Field label="名称" required hint="如 deepseek">
          <TextField value={draft.name} onChange={(v) => patch({ name: v })} placeholder="deepseek" />
        </Field>
        <Field label="显示名" hint="如 DeepSeek">
          <TextField
            value={draft.displayLabel}
            onChange={(v) => patch({ displayLabel: v })}
            placeholder="DeepSeek"
          />
        </Field>
        <Field label="Base URL" required colSpan={2} hint="如 https://api.example.com">
          <TextField
            value={draft.baseURL}
            onChange={(v) => patch({ baseURL: v })}
            placeholder="https://api.example.com"
            type="url"
          />
        </Field>
        <Field label="SDK 类型" required>
          <TabField
            value={draft.sdkKind}
            onChange={(v) => patch({ sdkKind: v as AdminProvider['sdkKind'] })}
            options={SDK_TAB_OPTIONS}
          />
        </Field>
        <Field label="协议" required>
          <SelectField
            value={draft.protocol}
            onChange={(v) => patch({ protocol: v })}
            options={PROTOCOL_OPTIONS}
          />
        </Field>
        <Field label="状态" colSpan={2}>
          <SwitchField
            checked={draft.status === 'active'}
            onChange={(v) => patch({ status: v ? 'active' : 'disabled' })}
            label={draft.status === 'active' ? '启用' : '停用'}
          />
        </Field>
      </FormSection>
    </Modal>
  );
}

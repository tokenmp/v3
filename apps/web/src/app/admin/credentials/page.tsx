'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import type { AdminProvider, AdminUpstreamCredential } from '@/types/admin';
import { FilterChip } from '@/components/filter-chip';
import { Modal } from '@/components/ui/modal';
import {
  Field,
  FormActions,
  FormSection,
  NumberField,
  SelectField,
  SwitchField,
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

type StatusFilter = 'all' | 'active' | 'disabled';

type CredentialDraft = {
  id: string;
  providerId: string;
  credentialRef: string;
  keyPrefix: string;
  keySuffix: string;
  priority: string;
  maxConcurrency: string;
  dailyQuota: string;
  status: AdminUpstreamCredential['status'];
};

function emptyDraft(): CredentialDraft {
  return {
    id: '',
    providerId: '',
    credentialRef: '',
    keyPrefix: '',
    keySuffix: '',
    priority: '0',
    maxConcurrency: '',
    dailyQuota: '',
    status: 'active',
  };
}

function toDraft(c: AdminUpstreamCredential): CredentialDraft {
  return {
    id: c.id,
    providerId: c.providerId,
    credentialRef: c.credentialRef,
    keyPrefix: c.keyPrefix ?? '',
    keySuffix: c.keySuffix ?? '',
    priority: String(c.priority ?? 0),
    maxConcurrency: c.maxConcurrency != null ? String(c.maxConcurrency) : '',
    dailyQuota: c.dailyQuota != null ? String(c.dailyQuota) : '',
    status: c.status === 'active' ? 'active' : 'disabled',
  };
}

function numOrNull(v: string): number | null {
  const t = v.trim();
  if (!t) return null;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}

export default function AdminCredentialsPage() {
  const queryClient = useQueryClient();

  const { data: providers = [], isLoading: providersLoading } = useQuery({
    queryKey: ['admin', 'providers'],
    queryFn: adminConfigApi.listProviders,
  });
  const { data: credentials = [], isLoading: credsLoading } = useQuery({
    queryKey: ['admin', 'credentials'],
    queryFn: adminConfigApi.listAllCredentials,
  });

  const providerMap = useMemo<Record<string, AdminProvider>>(() => {
    const m: Record<string, AdminProvider> = {};
    for (const p of providers) m[p.id] = p;
    return m;
  }, [providers]);

  const [search, setSearch] = useState('');
  const [statusF, setStatusF] = useState<StatusFilter>('all');
  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<AdminUpstreamCredential | null>(null);

  const isLoading = providersLoading || credsLoading;

  const filtered = useMemo(() => {
    const kw = search.trim().toLowerCase();
    return credentials.filter((c) => {
      const matchKw =
        !kw ||
        c.id.toLowerCase().includes(kw) ||
        c.credentialRef.toLowerCase().includes(kw) ||
        (c.keyPrefix ?? '').toLowerCase().includes(kw) ||
        c.providerId.toLowerCase().includes(kw);
      const matchStatus =
        statusF === 'all' ||
        (statusF === 'active' && c.status === 'active') ||
        (statusF === 'disabled' && c.status === 'disabled');
      return matchKw && matchStatus;
    });
  }, [credentials, search, statusF]);

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adminConfigApi.deleteCredential(id),
    onSuccess: () => {
      toast.success('已删除上游账号');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'credentials'] });
    },
    onError: (e: unknown) => {
      toast.error('删除失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const handleDelete = (c: AdminUpstreamCredential) => {
    if (!confirm(`删除上游账号「${c.id}」？`)) return;
    deleteMutation.mutate(c.id);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">上游账号</h1>

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          placeholder="搜索 id / ref / prefix / provider"
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
          新建账号
        </button>
      </div>

      {/* Table */}
      <div className="rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead>ID</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead>Credential Ref</TableHead>
                <TableHead>前缀/后缀</TableHead>
                <TableHead className="text-right">优先级</TableHead>
                <TableHead className="text-right">并发</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading ? (
                <TableRow>
                  <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                    加载中…
                  </TableCell>
                </TableRow>
              ) : filtered.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                    暂无上游账号
                  </TableCell>
                </TableRow>
              ) : (
                filtered.map((c) => {
                  const provider = providerMap[c.providerId];
                  const providerLabel = provider
                    ? provider.displayLabel || provider.name
                    : c.providerId;
                  const prefixSuffix =
                    c.keyPrefix || c.keySuffix
                      ? `${c.keyPrefix ?? '-'}…${c.keySuffix ?? '-'}`
                      : '-';
                  return (
                    <TableRow key={c.id}>
                      <TableCell className="font-mono text-xs">{c.id}</TableCell>
                      <TableCell className="text-sm">{providerLabel}</TableCell>
                      <TableCell
                        className="max-w-[260px] truncate font-mono text-xs"
                        title={c.credentialRef}
                      >
                        {c.credentialRef}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{prefixSuffix}</TableCell>
                      <TableCell className="text-right font-mono text-xs">{c.priority}</TableCell>
                      <TableCell className="text-right font-mono text-xs">
                        {c.maxConcurrency != null ? c.maxConcurrency : '∞'}
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={c.status} />
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <button
                            type="button"
                            onClick={() => setEditItem(c)}
                            className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                            aria-label="编辑"
                            title="编辑"
                          >
                            <Pencil className="size-3.5" />
                          </button>
                          <button
                            type="button"
                            onClick={() => handleDelete(c)}
                            className="rounded-sm p-1.5 text-muted-foreground hover:bg-red-100 hover:text-red-700"
                            aria-label="删除"
                            title="删除"
                          >
                            <Trash2 className="size-3.5" />
                          </button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {createOpen ? (
        <CredentialFormModal
          providers={providers}
          onClose={() => setCreateOpen(false)}
        />
      ) : null}
      {editItem ? (
        <CredentialFormModal
          editItem={editItem}
          providers={providers}
          onClose={() => setEditItem(null)}
        />
      ) : null}
    </div>
  );
}

function StatusBadge({ status }: { status: AdminUpstreamCredential['status'] }) {
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

function CredentialFormModal({
  editItem,
  providers,
  onClose,
}: {
  editItem?: AdminUpstreamCredential | null;
  providers: AdminProvider[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = !!editItem;
  const [draft, setDraft] = useState<CredentialDraft>(
    editItem ? toDraft(editItem) : emptyDraft(),
  );

  const patch = (p: Partial<CredentialDraft>) => setDraft((d) => ({ ...d, ...p }));

  const providerOptions = useMemo(
    () =>
      providers
        .filter((p) => p.status !== 'deleted')
        .map((p) => ({ value: p.id, label: p.displayLabel || p.name })),
    [providers],
  );

  const createMutation = useMutation({
    mutationFn: (input: CredentialDraft) =>
      adminConfigApi.createCredential(input.providerId, {
        id: input.id,
        credentialRef: input.credentialRef,
        keyPrefix: input.keyPrefix || undefined,
        keySuffix: input.keySuffix || undefined,
        priority: Number(input.priority) || 0,
        maxConcurrency: numOrNull(input.maxConcurrency),
        dailyQuota: numOrNull(input.dailyQuota),
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已创建上游账号');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'credentials'] });
      onClose();
    },
    onError: (e: unknown) => {
      toast.error('创建失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (input: CredentialDraft) =>
      adminConfigApi.updateCredential(editItem!.id, {
        credentialRef: input.credentialRef,
        keyPrefix: input.keyPrefix || null,
        keySuffix: input.keySuffix || null,
        priority: Number(input.priority) || 0,
        maxConcurrency: numOrNull(input.maxConcurrency),
        dailyQuota: numOrNull(input.dailyQuota),
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已更新上游账号');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'credentials'] });
      onClose();
    },
    onError: (e: unknown) => {
      toast.error('更新失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const submitting = createMutation.isPending || updateMutation.isPending;
  const canSubmit =
    draft.id.trim().length > 0 &&
    draft.providerId.length > 0 &&
    draft.credentialRef.trim().length > 0;

  const submit = () => {
    if (!canSubmit) return;
    if (isEdit) updateMutation.mutate(draft);
    else createMutation.mutate(draft);
  };

  return (
    <Modal
      open
      title={isEdit ? `编辑 ${editItem?.id ?? ''}` : '新建上游账号'}
      description="实际密钥通过 EXECUTOR_CREDENTIAL_* 环境变量映射，此处只保存 vault:// 引用与展示用前后缀。"
      onClose={onClose}
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={submit}
          submitting={submitting}
          submitLabel={isEdit ? '保存' : '创建'}
          disabled={!canSubmit}
        />
      }
    >
      <FormSection cols={2}>
        <Field label="Credential ID" required hint="唯一标识，创建后不可改" colSpan={2}>
          <TextField
            value={draft.id}
            onChange={(v) => patch({ id: v })}
            placeholder="deepseek-default"
            disabled={isEdit}
            className="font-mono"
          />
        </Field>
        <Field label="Provider" required hint={isEdit ? '所属 Provider，不可修改' : undefined}>
          <SelectField
            value={draft.providerId}
            onChange={(v) => patch({ providerId: v })}
            options={providerOptions}
            placeholder="请选择 Provider"
            disabled={isEdit}
          />
        </Field>
        <Field label="Credential Ref" required>
          <TextField
            value={draft.credentialRef}
            onChange={(v) => patch({ credentialRef: v })}
            placeholder="vault://provider/credential/default"
            className="font-mono"
          />
        </Field>
        <Field label="Key Prefix" hint="展示用前缀，如 sk-abc">
          <TextField
            value={draft.keyPrefix}
            onChange={(v) => patch({ keyPrefix: v })}
            placeholder="sk-abc"
            className="font-mono"
          />
        </Field>
        <Field label="Key Suffix" hint="展示用后缀，如 xyz">
          <TextField
            value={draft.keySuffix}
            onChange={(v) => patch({ keySuffix: v })}
            placeholder="xyz"
            className="font-mono"
          />
        </Field>
        <Field label="优先级" hint="数字越小优先级越高，默认 0">
          <NumberField value={draft.priority} onChange={(v) => patch({ priority: v })} />
        </Field>
        <Field label="最大并发" hint="留空=不限">
          <NumberField
            value={draft.maxConcurrency}
            onChange={(v) => patch({ maxConcurrency: v })}
            min={1}
            step={1}
            placeholder="不限"
          />
        </Field>
        <Field label="每日配额" hint="留空=不限">
          <NumberField
            value={draft.dailyQuota}
            onChange={(v) => patch({ dailyQuota: v })}
            min={0}
            step={1}
            placeholder="不限"
          />
        </Field>
        <Field label="状态" hint="启用或停用此账号">
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

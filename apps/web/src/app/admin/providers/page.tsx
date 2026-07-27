'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Network, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import type { AdminProvider, AdminUpstreamEndpoint } from '@/types/admin';
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
import { CompileButton } from '@/components/compile-button';
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

const AUTH_KIND_OPTIONS = [
  { value: 'bearer_header', label: 'Bearer Header' },
  { value: 'api_key_header', label: 'API Key Header' },
  { value: 'api_key_query', label: 'API Key Query' },
];

const PROTOCOL_LABELS: Record<string, string> = {
  openai_chat: 'Chat Completions',
  anthropic_messages: 'Messages',
  openai_responses: 'Responses',
  openai_images: 'Images',
};

/** Derive default protocol from SDK kind. */
function protocolForSdk(sdk: AdminProvider['sdkKind']): string {
  return sdk === 'anthropic' ? 'anthropic_messages' : 'openai_chat';
}

type StatusFilter = 'all' | 'active' | 'disabled';

type ProviderDraft = {
  id: string;
  name: string;
  displayLabel: string;
  baseURL: string;
  sdkKind: AdminProvider['sdkKind'];
  status: AdminProvider['status'];
};

function emptyDraft(): ProviderDraft {
  return {
    id: '',
    name: '',
    displayLabel: '',
    baseURL: '',
    sdkKind: 'openai',
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
  const [endpointsProvider, setEndpointsProvider] = useState<AdminProvider | null>(null);

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
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-semibold">Provider 管理</h1>
        <div className="ml-auto">
          <CompileButton size="sm" />
        </div>
      </div>

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
      <div className="hidden md:block rounded-md border border-border bg-card">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/30">
                <TableHead>名称</TableHead>
                <TableHead>显示名</TableHead>
                <TableHead>Base URL</TableHead>
                <TableHead>SDK</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading ? (
                <TableRow>
                  <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
                    加载中…
                  </TableCell>
                </TableRow>
              ) : filtered.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
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
                    <TableCell>
                      <StatusBadge status={p.status} />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <button
                          type="button"
                          onClick={() => setEndpointsProvider(p)}
                          className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                          aria-label="端点"
                          title="管理端点"
                        >
                          <Network className="size-3.5" />
                        </button>
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

      {/* Mobile cards */}
      <div className="md:hidden space-y-3">
        {isLoading ? (
          <p className="py-8 text-center text-sm text-muted-foreground">加载中…</p>
        ) : filtered.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">暂无 Provider</p>
        ) : (
          filtered.map((p) => (
            <div key={p.id} className="rounded-lg border bg-card p-3 space-y-2">
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <p className="text-sm font-medium truncate font-mono">{p.name}</p>
                  <p className="text-xs text-muted-foreground truncate">{p.displayLabel || '-'}</p>
                </div>
                <StatusBadge status={p.status} />
              </div>
              <p className="font-mono text-[10px] text-muted-foreground truncate" title={p.baseURL}>
                {p.baseURL}
              </p>
              <div className="flex items-center justify-between">
                <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {p.sdkKind}
                </span>
                <div className="flex gap-1">
                  <button
                    type="button"
                    onClick={() => setEndpointsProvider(p)}
                    className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                    aria-label="端点"
                    title="管理端点"
                  >
                    <Network className="size-3.5" />
                  </button>
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
              </div>
            </div>
          ))
        )}
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
      {endpointsProvider ? (
        <EndpointsModal
          provider={endpointsProvider}
          onClose={() => setEndpointsProvider(null)}
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
        protocol: protocolForSdk(input.sdkKind),
        displayLabel: input.displayLabel || undefined,
        selector: input.id,
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已创建 Provider（需点编译并发布生效）');
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
        protocol: protocolForSdk(input.sdkKind),
        status: input.status,
      }),
    onSuccess: () => {
      toast.success('已更新 Provider（需点编译并发布生效）');
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
      <div className="space-y-5">
        <FormSection title="基本信息" cols={2}>
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
        </FormSection>

        <FormSection title="SDK" cols={1} description="协议由 SDK 类型自动推导，具体端点在端点管理中配置">
          <Field label="SDK 类型" required hint={
            draft.sdkKind === 'openai'
              ? '→ 默认协议 openai_chat，可在端点管理中添加 responses/images 等端点'
              : '→ 默认协议 anthropic_messages'
          }>
            <TabField
              value={draft.sdkKind}
              onChange={(v) => patch({ sdkKind: v as AdminProvider['sdkKind'] })}
              options={SDK_TAB_OPTIONS}
            />
          </Field>
        </FormSection>

        <FormSection title="状态" cols={1}>
          <Field label="状态">
            <SwitchField
              checked={draft.status === 'active'}
              onChange={(v) => patch({ status: v ? 'active' : 'disabled' })}
              label={draft.status === 'active' ? '启用' : '停用'}
            />
          </Field>
        </FormSection>
      </div>
    </Modal>
  );
}

// ---- Endpoints Modal ----

const PROTOCOL_DEFAULT_PATHS: Record<string, string> = {
  openai_chat: '/v1/chat/completions',
  anthropic_messages: '/v1/messages',
  openai_responses: '/v1/responses',
  openai_images: '/v1/images/generations',
};

const DEFAULT_AUTH_HEADER: Record<string, string> = {
  bearer_header: 'Authorization',
  api_key_header: 'X-API-Key',
  api_key_query: '',
};

const DEFAULT_AUTH_PREFIX: Record<string, string> = {
  bearer_header: 'Bearer ',
  api_key_header: '',
  api_key_query: '',
};

type EndpointDraft = {
  id: number | null;
  path: string;
  protocol: string;
  authKind: AdminUpstreamEndpoint['authKind'];
  authHeader: string;
  authQuery: string;
  authPrefix: string;
  status: 'active' | 'disabled';
};

function emptyEndpointDraft(): EndpointDraft {
  return {
    id: null,
    path: '/v1/chat/completions',
    protocol: 'openai_chat',
    authKind: 'bearer_header',
    authHeader: 'Authorization',
    authQuery: '',
    authPrefix: 'Bearer ',
    status: 'active',
  };
}

function endpointToDraft(e: AdminUpstreamEndpoint): EndpointDraft {
  return {
    id: e.id,
    path: e.path,
    protocol: e.protocol,
    authKind: e.authKind,
    authHeader: e.authHeader ?? '',
    authQuery: e.authQuery ?? '',
    authPrefix: e.authPrefix ?? '',
    status: e.status === 'active' ? 'active' : 'disabled',
  };
}

function EndpointsModal({
  provider,
  onClose,
}: {
  provider: AdminProvider;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<EndpointDraft | null>(null);

  const { data: endpoints = [], isLoading } = useQuery({
    queryKey: ['admin', 'provider-endpoints', provider.id],
    queryFn: () => adminConfigApi.listEndpoints(provider.id),
  });

  const createMutation = useMutation({
    mutationFn: (d: EndpointDraft) =>
      adminConfigApi.createEndpoint(provider.id, {
        path: d.path,
        protocol: d.protocol,
        authKind: d.authKind,
        authHeader: d.authKind === 'api_key_query' ? null : (d.authHeader || null),
        authQuery: d.authKind === 'api_key_query' ? (d.authQuery || null) : null,
        authPrefix: d.authKind === 'bearer_header' ? (d.authPrefix || null) : null,
        status: d.status,
      }),
    onSuccess: () => {
      toast.success('已创建端点（需点编译并发布生效）');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'provider-endpoints', provider.id] });
      setEditing(null);
    },
    onError: (e: unknown) => {
      toast.error('创建失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (d: EndpointDraft) =>
      adminConfigApi.updateEndpoint(d.id!, {
        path: d.path,
        protocol: d.protocol,
        authKind: d.authKind,
        authHeader: d.authKind === 'api_key_query' ? null : (d.authHeader || null),
        authQuery: d.authKind === 'api_key_query' ? (d.authQuery || null) : null,
        authPrefix: d.authKind === 'bearer_header' ? (d.authPrefix || null) : null,
        status: d.status,
      }),
    onSuccess: () => {
      toast.success('已更新端点（需点编译并发布生效）');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'provider-endpoints', provider.id] });
      setEditing(null);
    },
    onError: (e: unknown) => {
      toast.error('更新失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => adminConfigApi.deleteEndpoint(id),
    onSuccess: () => {
      toast.success('已删除端点');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'provider-endpoints', provider.id] });
    },
    onError: (e: unknown) => {
      toast.error('删除失败', { description: e instanceof Error ? e.message : undefined });
    },
  });

  const handleDelete = (e: AdminUpstreamEndpoint) => {
    if (!confirm(`删除端点「${e.path}」？`)) return;
    deleteMutation.mutate(e.id);
  };

  const onAuthKindChange = (v: string) => {
    setEditing((d) => ({
      ...d!,
      authKind: v as EndpointDraft['authKind'],
      authHeader: DEFAULT_AUTH_HEADER[v] ?? d!.authHeader,
      authPrefix: DEFAULT_AUTH_PREFIX[v] ?? d!.authPrefix,
    }));
  };

  const onProtocolChange = (v: string) => {
    setEditing((d) => ({
      ...d!,
      protocol: v,
      path: d!.id === null ? (PROTOCOL_DEFAULT_PATHS[v] ?? d!.path) : d!.path,
    }));
  };

  const submitting = createMutation.isPending || updateMutation.isPending;
  const isEditEndpoint = editing?.id != null;
  const canSubmit = editing != null && editing.path.trim().length > 0 && editing.protocol.length > 0;

  const submit = () => {
    if (!editing || !canSubmit) return;
    if (isEditEndpoint) updateMutation.mutate(editing);
    else createMutation.mutate(editing);
  };

  return (
    <Modal
      open
      title={`${provider.displayLabel || provider.name} — 端点管理`}
      description="每个 Provider 可配置多个协议端点。Executor 按 route.protocol 选择匹配的端点转发。"
      onClose={onClose}
      maxWidth="lg"
    >
      {editing ? (
        <div className="space-y-5">
          <FormSection title={isEditEndpoint ? '编辑端点' : '新建端点'} cols={2}>
            <Field label="协议" required>
              <SelectField
                value={editing.protocol}
                onChange={onProtocolChange}
                options={PROTOCOL_OPTIONS}
                disabled={isEditEndpoint}
              />
            </Field>
            <Field label="路径" required hint="相对于 Base URL 的路径">
              <TextField
                value={editing.path}
                onChange={(v) => setEditing({ ...editing, path: v })}
                placeholder="/v1/chat/completions"
                className="font-mono"
              />
            </Field>
            <Field label="鉴权方式" required>
              <SelectField
                value={editing.authKind}
                onChange={onAuthKindChange}
                options={AUTH_KIND_OPTIONS}
                disabled={isEditEndpoint}
              />
            </Field>
            {editing.authKind === 'api_key_query' ? (
              <Field label="Query 参数名" required hint="如 key">
                <TextField
                  value={editing.authQuery}
                  onChange={(v) => setEditing({ ...editing, authQuery: v })}
                  placeholder="key"
                />
              </Field>
            ) : (
              <Field label="Header 名称" required hint="如 Authorization">
                <TextField
                  value={editing.authHeader}
                  onChange={(v) => setEditing({ ...editing, authHeader: v })}
                  placeholder="Authorization"
                />
              </Field>
            )}
            {editing.authKind === 'bearer_header' ? (
              <Field label="前缀" hint="如 Bearer （含空格）">
                <TextField
                  value={editing.authPrefix}
                  onChange={(v) => setEditing({ ...editing, authPrefix: v })}
                  placeholder="Bearer "
                />
              </Field>
            ) : null}
            <Field label="状态">
              <SwitchField
                checked={editing.status === 'active'}
                onChange={(v) => setEditing({ ...editing, status: v ? 'active' : 'disabled' })}
                label={editing.status === 'active' ? '启用' : '停用'}
              />
            </Field>
          </FormSection>
          <FormActions
            onCancel={() => setEditing(null)}
            onSubmit={submit}
            submitting={submitting}
            submitLabel={isEditEndpoint ? '保存' : '创建'}
            disabled={!canSubmit}
          />
        </div>
      ) : (
        <div className="space-y-3">
          {isLoading ? (
            <p className="py-8 text-center text-sm text-muted-foreground">加载中…</p>
          ) : endpoints.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无端点，点击「新建端点」添加</p>
          ) : (
            <div className="rounded-md border border-border">
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow className="bg-muted/30">
                      <TableHead className="text-xs">协议</TableHead>
                      <TableHead className="text-xs">路径</TableHead>
                      <TableHead className="text-xs">鉴权</TableHead>
                      <TableHead className="text-xs">状态</TableHead>
                      <TableHead className="text-right text-xs">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {endpoints.map((e) => (
                      <TableRow key={e.id}>
                        <TableCell className="text-xs">
                          <span className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[10px]">
                            {PROTOCOL_LABELS[e.protocol] ?? e.protocol}
                          </span>
                        </TableCell>
                        <TableCell className="font-mono text-xs">{e.path}</TableCell>
                        <TableCell className="font-mono text-[10px] text-muted-foreground">
                          {e.authKind === 'api_key_query'
                            ? `?${e.authQuery ?? ''}=`
                            : `${e.authHeader ?? ''}${e.authPrefix ? ` (${e.authPrefix.trim()})` : ''}`}
                        </TableCell>
                        <TableCell>
                          <span
                            className={cn(
                              'inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium',
                              e.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-muted text-muted-foreground',
                            )}
                          >
                            {e.status === 'active' ? '启用' : '停用'}
                          </span>
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex justify-end gap-1">
                            <button
                              type="button"
                              onClick={() => setEditing(endpointToDraft(e))}
                              className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                              aria-label="编辑"
                              title="编辑"
                            >
                              <Pencil className="size-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDelete(e)}
                              className="rounded-sm p-1.5 text-muted-foreground hover:bg-red-100 hover:text-red-700"
                              aria-label="删除"
                              title="删除"
                            >
                              <Trash2 className="size-3.5" />
                            </button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
          <div>
            <button
              type="button"
              onClick={() => setEditing(emptyEndpointDraft())}
              className="inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
            >
              <Plus className="size-3.5" />
              新建端点
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}

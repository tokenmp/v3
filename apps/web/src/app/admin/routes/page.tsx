'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
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
  inputCls,
} from '@/components/ui/field';
import { PublishStatusHint } from '@/components/publish-status-hint';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { AdminRouteConfig } from '@/types/admin';

const PROTOCOL_OPTIONS = [
  'openai_chat',
  'anthropic_messages',
  'openai_responses',
  'openai_images',
];

type StatusFilter = 'all' | 'active' | 'disabled';

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '正常' },
  { key: 'disabled', label: '已禁用' },
];

function StatusPill({ enabled, quarantined }: { enabled: boolean; quarantined: boolean }) {
  let cls = 'bg-green-100 text-green-700';
  let label = '正常';
  if (quarantined) {
    cls = 'bg-amber-100 text-amber-700';
    label = '已隔离';
  } else if (!enabled) {
    cls = 'bg-muted text-muted-foreground';
    label = '已禁用';
  }
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${cls}`}>
      {label}
    </span>
  );
}

type RouteDraft = {
  id: string;
  modelId: string;
  providerId: string;
  upstreamModel: string;
  protocol: string;
  priority: number;
  enabled: boolean;
  contextWindow: number | null;
  maxOutputTokens: number | null;
};

function emptyDraft(): RouteDraft {
  return {
    id: '',
    modelId: '',
    providerId: '',
    upstreamModel: '',
    protocol: 'openai_chat',
    priority: 0,
    enabled: true,
    contextWindow: null,
    maxOutputTokens: null,
  };
}

function fromRoute(r: AdminRouteConfig): RouteDraft {
  return {
    id: r.id,
    modelId: r.modelId,
    providerId: r.providerId,
    upstreamModel: r.upstreamModel,
    protocol: r.protocol,
    priority: r.priority,
    enabled: r.enabled,
    contextWindow: r.contextWindow,
    maxOutputTokens: r.maxOutputTokens,
  };
}

export default function AdminRoutesPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<AdminRouteConfig | null>(null);

  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });

  const createMut = useMutation({
    mutationFn: (input: RouteDraft) =>
      adminConfigApi.createRoute({
        id: input.id,
        modelId: input.modelId,
        providerId: input.providerId,
        upstreamModel: input.upstreamModel,
        protocol: input.protocol,
        priority: input.priority,
        contextWindow: input.contextWindow,
        maxOutputTokens: input.maxOutputTokens,
      }),
    onSuccess: () => {
      toast.success('路由已创建（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      setCreating(false);
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '创建失败');
    },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, input }: { id: string; input: RouteDraft }) =>
      adminConfigApi.updateRoute(id, {
        modelId: input.modelId,
        providerId: input.providerId,
        upstreamModel: input.upstreamModel,
        protocol: input.protocol,
        priority: input.priority,
        enabled: input.enabled,
        contextWindow: input.contextWindow,
        maxOutputTokens: input.maxOutputTokens,
      }),
    onSuccess: () => {
      toast.success('路由已保存（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      setEditing(null);
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '保存失败');
    },
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => adminConfigApi.deleteRoute(id),
    onSuccess: () => {
      toast.success('路由已删除（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '删除失败');
    },
  });

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return routes.filter((r) => {
      if (statusFilter === 'active' && !(r.enabled && !r.quarantined)) return false;
      if (statusFilter === 'disabled' && r.enabled) return false;
      if (!q) return true;
      return (
        r.id.toLowerCase().includes(q) ||
        r.modelId.toLowerCase().includes(q) ||
        r.providerId.toLowerCase().includes(q) ||
        r.upstreamModel.toLowerCase().includes(q)
      );
    });
  }, [routes, search, statusFilter]);

  const onDelete = (r: AdminRouteConfig) => {
    if (!confirm(`确定删除路由「${r.id}」？`)) return;
    deleteMut.mutate(r.id);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-semibold">路由管理</h1>
        <div className="ml-auto">
          <PublishStatusHint />
        </div>
      </div>

      {/* 工具栏：搜索 + 状态筛选 + 新建 */}
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索 ID / 模型 / Provider / 上游模型"
          className={`${inputCls} max-w-xs`}
        />
        <div className="flex flex-wrap gap-1.5">
          {STATUS_FILTERS.map((f) => (
            <FilterChip
              key={f.key}
              label={f.label}
              active={statusFilter === f.key}
              onClick={() => setStatusFilter(f.key)}
            />
          ))}
        </div>
        <div className="ml-auto">
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
          >
            <Plus className="size-3.5" />
            新建路由
          </button>
        </div>
      </div>

      {/* 表格 */}
      {isLoading ? (
        <div className="py-12 text-center text-sm text-muted-foreground">加载中…</div>
      ) : filtered.length === 0 ? (
        <div className="py-12 text-center text-sm text-muted-foreground">暂无路由配置</div>
      ) : (
        <>
        <div className="hidden md:block overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>模型</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead>上游模型</TableHead>
                <TableHead>协议</TableHead>
                <TableHead>优先级</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-mono text-xs">{r.id}</TableCell>
                  <TableCell className="font-mono text-xs">{r.modelId}</TableCell>
                  <TableCell className="font-mono text-xs">{r.providerId}</TableCell>
                  <TableCell className="font-mono text-xs">{r.upstreamModel}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{r.protocol}</TableCell>
                  <TableCell className="tabular-nums">{r.priority}</TableCell>
                  <TableCell>
                    <StatusPill enabled={r.enabled} quarantined={r.quarantined} />
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <button
                        type="button"
                        onClick={() => setEditing(r)}
                        className="rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                        aria-label="编辑"
                        title="编辑"
                      >
                        <Pencil className="size-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick={() => onDelete(r)}
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

        {/* 移动端卡片 */}
        <div className="md:hidden space-y-3">
          {filtered.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无路由配置</p>
          ) : (
            filtered.map((r) => (
              <button
                key={r.id}
                type="button"
                onClick={() => setEditing(r)}
                className="w-full text-left rounded-lg border bg-card p-3 space-y-2 active:bg-accent/50 transition-colors"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs truncate">{r.id}</span>
                  <StatusPill enabled={r.enabled} quarantined={r.quarantined} />
                </div>
                <div className="flex items-center justify-between text-xs">
                  <span className="font-medium truncate">{r.modelId}</span>
                  <span className="text-muted-foreground">{r.protocol}</span>
                </div>
                <p className="text-xs text-muted-foreground">
                  {r.providerId} → {r.upstreamModel}
                </p>
                <span className="text-xs text-muted-foreground">优先级 {r.priority}</span>
              </button>
            ))
          )}
        </div>
        </>
      )}

      {creating ? (
        <RouteFormModal
          onClose={() => setCreating(false)}
          onSubmit={(d) => createMut.mutate(d)}
          submitting={createMut.isPending}
        />
      ) : null}
      {editing ? (
        <RouteFormModal
          initial={editing}
          onClose={() => setEditing(null)}
          onSubmit={(d) => updateMut.mutate({ id: editing.id, input: d })}
          submitting={updateMut.isPending}
        />
      ) : null}
    </div>
  );
}

function RouteFormModal({
  initial,
  onClose,
  onSubmit,
  submitting,
}: {
  initial?: AdminRouteConfig;
  onClose: () => void;
  onSubmit: (d: RouteDraft) => void;
  submitting?: boolean;
}) {
  const isEdit = !!initial;
  const [draft, setDraft] = useState<RouteDraft>(initial ? fromRoute(initial) : emptyDraft());
  const [contextWindowStr, setContextWindowStr] = useState<string>(
    initial?.contextWindow != null ? String(initial.contextWindow) : '',
  );
  const [maxOutputTokensStr, setMaxOutputTokensStr] = useState<string>(
    initial?.maxOutputTokens != null ? String(initial.maxOutputTokens) : '',
  );

  const { data: providers = [] } = useQuery({
    queryKey: ['admin', 'providers'],
    queryFn: adminConfigApi.listProviders,
  });
  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'model-configs'],
    queryFn: adminConfigApi.listModels,
  });

  const providerOptions = providers.map((p) => ({
    value: p.id,
    label: p.displayLabel || p.name,
  }));
  const modelOptions = models.map((m) => ({
    value: m.id,
    label: m.displayName || m.id,
  }));

  /** Parse empty string → null, non-empty → number */
  const parseNullableInt = (v: string): number | null => {
    const trimmed = v.trim();
    if (trimmed === '') return null;
    const n = Number(trimmed);
    return Number.isNaN(n) ? null : n;
  };

  const canSubmit =
    draft.id.trim() !== '' &&
    draft.modelId !== '' &&
    draft.providerId !== '' &&
    draft.upstreamModel.trim() !== '' &&
    draft.protocol !== '';

  const submit = () => {
    if (!canSubmit) return;
    const finalDraft: RouteDraft = {
      ...draft,
      contextWindow: parseNullableInt(contextWindowStr),
      maxOutputTokens: parseNullableInt(maxOutputTokensStr),
    };
    onSubmit(finalDraft);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={isEdit ? '编辑路由' : '新建路由'}
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={submit}
          submitLabel={isEdit ? '保存' : '创建'}
          submitting={submitting}
          disabled={!canSubmit}
        />
      }
    >
      <div className="space-y-5">
        <FormSection title="基本信息" cols={2}>
          <Field label="路由 ID" required>
            <TextField
              value={draft.id}
              onChange={(v) => setDraft((d) => ({ ...d, id: v }))}
              placeholder="例如 openai-gpt-4o-mini"
              disabled={isEdit}
              className="font-mono"
            />
          </Field>
          <Field label="上游模型" required>
            <TextField
              value={draft.upstreamModel}
              onChange={(v) => setDraft((d) => ({ ...d, upstreamModel: v }))}
              placeholder="例如 deepseek-chat"
              className="font-mono"
            />
          </Field>
        </FormSection>

        <FormSection title="路由绑定" cols={2}>
          <Field label="模型" required>
            <SelectField
              value={draft.modelId}
              onChange={(v) => setDraft((d) => ({ ...d, modelId: v }))}
              options={modelOptions}
              placeholder="选择模型"
            />
          </Field>
          <Field label="Provider" required>
            <SelectField
              value={draft.providerId}
              onChange={(v) => setDraft((d) => ({ ...d, providerId: v }))}
              options={providerOptions}
              placeholder="选择 Provider"
            />
          </Field>
        </FormSection>

        <FormSection title="协议与调度" cols={3}>
          <Field label="协议" required>
            <SelectField
              value={draft.protocol}
              onChange={(v) => setDraft((d) => ({ ...d, protocol: v }))}
              options={PROTOCOL_OPTIONS}
            />
          </Field>
          <Field label="优先级" hint="数字越小优先级越高">
            <NumberField
              value={String(draft.priority)}
              onChange={(v) => setDraft((d) => ({ ...d, priority: Number(v) || 0 }))}
              min={0}
            />
          </Field>
          <Field label="启用">
            <SwitchField
              checked={draft.enabled}
              onChange={(v) => setDraft((d) => ({ ...d, enabled: v }))}
              label={draft.enabled ? '已启用' : '已禁用'}
            />
          </Field>
        </FormSection>

        <FormSection title="容量覆盖" cols={2} description="留空表示继承模型默认值">
          <Field label="上下文窗口（token）" hint="留空继承模型默认">
            <NumberField
              value={contextWindowStr}
              onChange={setContextWindowStr}
              placeholder="留空继承模型默认"
              min={1}
            />
          </Field>
          <Field label="最大输出 Token" hint="留空继承模型默认">
            <NumberField
              value={maxOutputTokensStr}
              onChange={setMaxOutputTokensStr}
              placeholder="留空继承模型默认"
              min={1}
            />
          </Field>
        </FormSection>
      </div>
    </Modal>
  );
}

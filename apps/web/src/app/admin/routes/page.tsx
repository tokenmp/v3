'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Pencil, Plus } from 'lucide-react';
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
import type { AdminRouteConfig, AdminRouteCredential } from '@/types/admin';

const PROTOCOL_OPTIONS = [
  'openai_chat',
  'anthropic_messages',
  'openai_responses',
  'openai_images',
];

type StatusFilter = 'all' | 'active' | 'disabled';

type RouteProviderGroup = {
  key: string;
  modelId: string;
  providerId: string;
  routes: AdminRouteConfig[];
};

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
  rpm: number | null;
  tpm: number | null;
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
    rpm: null,
    tpm: null,
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
    rpm: r.rpm,
    tpm: r.tpm,
  };
}

export default function AdminRoutesPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<AdminRouteConfig | null>(null);
  const [providerEditor, setProviderEditor] = useState<RouteProviderGroup | null>(null);
  const [accountRoute, setAccountRoute] = useState<AdminRouteConfig | null>(null);

  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });
  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'model-configs'],
    queryFn: adminConfigApi.listModels,
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
        rpm: input.rpm,
        tpm: input.tpm,
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
        rpm: input.rpm,
        tpm: input.tpm,
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

  const routeModelOptions = useMemo(() => {
    const ids = new Set<string>(models.map((m) => m.id));
    routes.forEach((r) => ids.add(r.modelId));
    return Array.from(ids).sort((a, b) => a.localeCompare(b));
  }, [models, routes]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return routes.filter((r) => {
      if (selectedModel !== 'all' && r.modelId !== selectedModel) return false;
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
  }, [routes, search, selectedModel, statusFilter]);

  const modelSummary = useMemo(() => {
    const providers = new Set(filtered.map((r) => r.providerId));
    const protocols = new Set(filtered.map((r) => r.protocol));
    return { providers: providers.size, protocols: protocols.size, routes: filtered.length };
  }, [filtered]);

  const providerGroups = useMemo<RouteProviderGroup[]>(() => {
    const groups = new Map<string, RouteProviderGroup>();
    for (const route of filtered) {
      const key = `${route.modelId}::${route.providerId}`;
      const existing = groups.get(key);
      if (existing) {
        existing.routes.push(route);
      } else {
        groups.set(key, {
          key,
          modelId: route.modelId,
          providerId: route.providerId,
          routes: [route],
        });
      }
    }
    return Array.from(groups.values()).sort((a, b) => {
      const modelCmp = a.modelId.localeCompare(b.modelId);
      if (modelCmp !== 0) return modelCmp;
      return a.providerId.localeCompare(b.providerId);
    });
  }, [filtered]);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-semibold">路由管理</h1>
        <div className="ml-auto">
          <PublishStatusHint />
        </div>
      </div>

      {/* 工具栏：模型过滤 + 搜索 + 状态筛选 + 新建 */}
      <div className="flex flex-wrap items-center gap-2">
        <select
          value={selectedModel}
          onChange={(e) => setSelectedModel(e.target.value)}
          className={`${inputCls} w-full sm:w-72 font-mono`}
          aria-label="按模型过滤路由"
        >
          <option value="all">全部模型</option>
          {routeModelOptions.map((id) => (
            <option key={id} value={id}>{id}</option>
          ))}
        </select>
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索 Provider / 上游模型 / 路由 ID"
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

      <div className="rounded-lg border bg-card p-3 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">当前视图：</span>
        {selectedModel === 'all' ? '全部模型' : selectedModel} · {modelSummary.routes} 条路由 · {modelSummary.providers} 个 Provider · {modelSummary.protocols} 个协议。
        <span className="ml-1">一个模型可以绑定多个 Provider；每条 Provider+协议路由可再绑定多个账号候选。</span>
      </div>

      {/* Provider 分组列表 */}
      {isLoading ? (
        <div className="py-12 text-center text-sm text-muted-foreground">加载中…</div>
      ) : providerGroups.length === 0 ? (
        <div className="py-12 text-center text-sm text-muted-foreground">暂无路由配置</div>
      ) : (
        <div className="grid gap-3">
          {providerGroups.map((group) => (
            <ProviderRouteCard
              key={group.key}
              group={group}
              onEdit={() => setProviderEditor(group)}
              onConfigureAccounts={(route) => setAccountRoute(route)}
            />
          ))}
        </div>
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
      {providerEditor ? (
        <ProviderRouteModal
          group={providerEditor}
          onClose={() => setProviderEditor(null)}
          onConfigureAccounts={(route) => setAccountRoute(route)}
        />
      ) : null}
      {accountRoute ? (
        <RouteAccountsModal route={accountRoute} onClose={() => setAccountRoute(null)} />
      ) : null}
    </div>
  );
}

function ProviderRouteCard({
  group,
  onEdit,
  onConfigureAccounts,
}: {
  group: RouteProviderGroup;
  onEdit: () => void;
  onConfigureAccounts: (route: AdminRouteConfig) => void;
}) {
  const activeRoutes = group.routes.filter((r) => r.enabled && !r.quarantined);
  const disabledRoutes = group.routes.filter((r) => !r.enabled || r.quarantined);
  const protocols = group.routes.map((r) => r.protocol).sort();
  return (
    <section className="rounded-lg border bg-card p-4 shadow-sm">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-sm font-semibold">{group.providerId}</h2>
            <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
              {group.modelId}
            </span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {activeRoutes.length} 个启用协议 · {disabledRoutes.length} 个关闭/隔离协议 · {protocols.length} 个已配置协议
          </p>
        </div>
        <button
          type="button"
          onClick={onEdit}
          className="inline-flex h-[var(--control-height-sm)] items-center gap-1.5 rounded-sm border px-3 text-xs font-medium hover:bg-accent"
        >
          <Pencil className="size-3.5" /> 编辑 Provider 路由
        </button>
      </div>

      <div className="mt-3 grid gap-2 md:grid-cols-3">
        {PROTOCOL_OPTIONS.map((protocol) => {
          const route = group.routes.find((r) => r.protocol === protocol);
          return (
            <div key={protocol} className="rounded-md border bg-muted/20 p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-xs">{protocol}</span>
                {route ? <StatusPill enabled={route.enabled} quarantined={route.quarantined} /> : (
                  <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">未配置</span>
                )}
              </div>
              <div className="mt-2 min-h-8 text-xs text-muted-foreground">
                {route ? (
                  <>
                    <div className="truncate font-mono">{route.upstreamModel}</div>
                    <div>优先级 {route.priority} · TPM {route.tpm != null ? route.tpm.toLocaleString() : '继承'}</div>
                  </>
                ) : '可在编辑中开启该协议'}
              </div>
              {route ? (
                <button
                  type="button"
                  onClick={() => onConfigureAccounts(route)}
                  className="mt-2 inline-flex items-center gap-1 text-xs text-primary hover:underline"
                >
                  <KeyRound className="size-3" /> 账号候选
                </button>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function ProviderRouteModal({
  group,
  onClose,
  onConfigureAccounts,
}: {
  group: RouteProviderGroup;
  onClose: () => void;
  onConfigureAccounts: (route: AdminRouteConfig) => void;
}) {
  const queryClient = useQueryClient();
  const routeByProtocol = useMemo(() => new Map(group.routes.map((r) => [r.protocol, r])), [group.routes]);
  const [drafts, setDrafts] = useState<Record<string, RouteDraft>>(() => {
    const initial: Record<string, RouteDraft> = {};
    for (const protocol of PROTOCOL_OPTIONS) {
      const route = routeByProtocol.get(protocol);
      initial[protocol] = route
        ? fromRoute(route)
        : defaultRouteDraft(group.modelId, group.providerId, protocol);
    }
    return initial;
  });
  const [limitInputs, setLimitInputs] = useState<Record<string, { contextWindow: string; maxOutputTokens: string; rpm: string; tpm: string }>>(() => {
    const initial: Record<string, { contextWindow: string; maxOutputTokens: string; rpm: string; tpm: string }> = {};
    for (const protocol of PROTOCOL_OPTIONS) {
      const route = routeByProtocol.get(protocol);
      initial[protocol] = route
        ? {
            contextWindow: route.contextWindow != null ? String(route.contextWindow) : '',
            maxOutputTokens: route.maxOutputTokens != null ? String(route.maxOutputTokens) : '',
            rpm: route.rpm != null ? String(route.rpm) : '',
            tpm: route.tpm != null ? String(route.tpm) : '',
          }
        : emptyLimitInputs();
    }
    return initial;
  });

  const parseNullableInt = (v: string): number | null => {
    const trimmed = v.trim();
    if (trimmed === '') return null;
    const n = Number(trimmed);
    return Number.isNaN(n) ? null : n;
  };

  const saveMut = useMutation({
    mutationFn: async () => {
      for (const protocol of PROTOCOL_OPTIONS) {
        const draft = drafts[protocol] ?? defaultRouteDraft(group.modelId, group.providerId, protocol);
        const limits = limitInputs[protocol] ?? emptyLimitInputs();
        const finalDraft: RouteDraft = {
          ...draft,
          contextWindow: parseNullableInt(limits.contextWindow),
          maxOutputTokens: parseNullableInt(limits.maxOutputTokens),
          rpm: parseNullableInt(limits.rpm),
          tpm: parseNullableInt(limits.tpm),
        };
        const existing = routeByProtocol.get(protocol);
        if (existing) {
          await adminConfigApi.updateRoute(existing.id, {
            modelId: finalDraft.modelId,
            providerId: finalDraft.providerId,
            upstreamModel: finalDraft.upstreamModel,
            protocol: finalDraft.protocol,
            priority: finalDraft.priority,
            enabled: finalDraft.enabled,
            contextWindow: finalDraft.contextWindow,
            maxOutputTokens: finalDraft.maxOutputTokens,
            rpm: finalDraft.rpm,
            tpm: finalDraft.tpm,
          });
        } else if (finalDraft.enabled) {
          await adminConfigApi.createRoute({
            id: finalDraft.id,
            modelId: finalDraft.modelId,
            providerId: finalDraft.providerId,
            upstreamModel: finalDraft.upstreamModel,
            protocol: finalDraft.protocol,
            priority: finalDraft.priority,
            contextWindow: finalDraft.contextWindow,
            maxOutputTokens: finalDraft.maxOutputTokens,
            rpm: finalDraft.rpm,
            tpm: finalDraft.tpm,
          });
        }
      }
    },
    onSuccess: () => {
      toast.success('Provider 路由协议已保存（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const patchDraft = (protocol: string, patch: Partial<RouteDraft>) => {
    setDrafts((current) => ({
      ...current,
      [protocol]: { ...(current[protocol] ?? defaultRouteDraft(group.modelId, group.providerId, protocol)), ...patch },
    }));
  };
  const patchLimit = (protocol: string, patch: Partial<{ contextWindow: string; maxOutputTokens: string; rpm: string; tpm: string }>) => {
    setLimitInputs((current) => ({
      ...current,
      [protocol]: { ...(current[protocol] ?? emptyLimitInputs()), ...patch },
    }));
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="编辑 Provider 的协议路由"
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => saveMut.mutate()}
          submitLabel="保存协议配置"
          submitting={saveMut.isPending}
        />
      }
    >
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          在这里开启/关闭 OpenAI Chat、Responses、Anthropic Messages 等协议；账号候选可按协议路由单独配置，也可共用 Provider 下账号。
        </div>
        <div className="space-y-3">
          {PROTOCOL_OPTIONS.map((protocol) => {
            const draft = drafts[protocol] ?? defaultRouteDraft(group.modelId, group.providerId, protocol);
            const limits = limitInputs[protocol] ?? emptyLimitInputs();
            const existing = routeByProtocol.get(protocol);
            return (
              <section key={protocol} className="rounded-lg border p-3">
                <div className="flex flex-wrap items-center gap-3">
                  <SwitchField
                    checked={draft.enabled}
                    onChange={(v) => patchDraft(protocol, { enabled: v })}
                    label={draft.enabled ? '开启' : '关闭'}
                  />
                  <div>
                    <div className="font-mono text-sm font-medium">{protocol}</div>
                    <div className="text-xs text-muted-foreground">{existing ? existing.id : '开启后自动创建路由'}</div>
                  </div>
                  {existing ? (
                    <button
                      type="button"
                      onClick={() => onConfigureAccounts(existing)}
                      className="ml-auto inline-flex items-center gap-1 text-xs text-primary hover:underline"
                    >
                      <KeyRound className="size-3" /> 配置账号
                    </button>
                  ) : null}
                </div>
                <div className="mt-3 grid gap-3 md:grid-cols-3">
                  <Field label="上游模型">
                    <TextField
                      value={draft.upstreamModel}
                      onChange={(v) => patchDraft(protocol, { upstreamModel: v })}
                      disabled={!draft.enabled}
                      className="font-mono"
                    />
                  </Field>
                  <Field label="优先级" hint="数字越小优先级越高">
                    <NumberField
                      value={String(draft.priority)}
                      onChange={(v) => patchDraft(protocol, { priority: Number(v) || 0 })}
                      min={0}
                      disabled={!draft.enabled}
                    />
                  </Field>
                  <Field label="路由状态">
                    <StatusPill enabled={draft.enabled} quarantined={existing?.quarantined ?? false} />
                  </Field>
                </div>
                <div className="mt-3 grid gap-3 md:grid-cols-4">
                  <Field label="Context" hint="留空继承">
                    <NumberField value={limits.contextWindow} onChange={(v) => patchLimit(protocol, { contextWindow: v })} min={1} disabled={!draft.enabled} />
                  </Field>
                  <Field label="Max Output" hint="留空继承">
                    <NumberField value={limits.maxOutputTokens} onChange={(v) => patchLimit(protocol, { maxOutputTokens: v })} min={1} disabled={!draft.enabled} />
                  </Field>
                  <Field label="RPM" hint="留空继承">
                    <NumberField value={limits.rpm} onChange={(v) => patchLimit(protocol, { rpm: v })} min={1} disabled={!draft.enabled} />
                  </Field>
                  <Field label="TPM" hint="留空继承">
                    <NumberField value={limits.tpm} onChange={(v) => patchLimit(protocol, { tpm: v })} min={1} disabled={!draft.enabled} />
                  </Field>
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </Modal>
  );
}

function defaultRouteDraft(modelId: string, providerId: string, protocol: string): RouteDraft {
  return {
    ...emptyDraft(),
    id: routeIDFor(modelId, providerId, protocol),
    modelId,
    providerId,
    upstreamModel: modelId,
    protocol,
    enabled: false,
  };
}

function emptyLimitInputs() {
  return { contextWindow: '', maxOutputTokens: '', rpm: '', tpm: '' };
}

function routeIDFor(modelId: string, providerId: string, protocol: string): string {
  const safe = `${modelId}-${providerId}-${protocol}`
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return `route-${safe}`.slice(0, 120);
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
  const [rpmStr, setRPMStr] = useState<string>(initial?.rpm != null ? String(initial.rpm) : '');
  const [tpmStr, setTPMStr] = useState<string>(initial?.tpm != null ? String(initial.tpm) : '');

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
      rpm: parseNullableInt(rpmStr),
      tpm: parseNullableInt(tpmStr),
    };
    onSubmit(finalDraft);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={isEdit ? '编辑模型路由目标' : '新建模型路由目标'}
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
        <FormSection title="指定模型与上游目标" cols={2} description="先选择对外模型，再为该模型添加一个 Provider + 协议目标；同一模型可添加多条路由。">
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

        <FormSection title="Provider 与账号协议" cols={2} description="Provider 表示供应商/账号池；协议决定使用哪个 endpoint/adapter。账号候选在列表的“配置账号”里绑定。">
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

        <FormSection title="Provider+模型覆盖" cols={2} description="留空表示继承 Provider 默认值；用于某个模型在某个供应商下的上下文、输出和 RPM/TPM 覆盖。">
          <Field label="上下文窗口（token）" hint="留空继承 Provider 默认">
            <NumberField
              value={contextWindowStr}
              onChange={setContextWindowStr}
              placeholder="留空继承 Provider 默认"
              min={1}
            />
          </Field>
          <Field label="最大输出 Token" hint="留空继承 Provider 默认">
            <NumberField
              value={maxOutputTokensStr}
              onChange={setMaxOutputTokensStr}
              placeholder="留空继承 Provider 默认"
              min={1}
            />
          </Field>
          <Field label="RPM" hint="留空继承 Provider/账号设置">
            <NumberField value={rpmStr} onChange={setRPMStr} placeholder="例如 500" min={1} />
          </Field>
          <Field label="TPM" hint="留空继承 Provider/账号设置">
            <NumberField value={tpmStr} onChange={setTPMStr} placeholder="例如 1000000" min={1} />
          </Field>
        </FormSection>
      </div>
    </Modal>
  );
}

function RouteAccountsModal({ route, onClose }: { route: AdminRouteConfig; onClose: () => void }) {
  const queryClient = useQueryClient();
  const { data: providerCredentials = [], isLoading: loadingCredentials } = useQuery({
    queryKey: ['admin', 'provider-credentials', route.providerId],
    queryFn: () => adminConfigApi.listCredentials(route.providerId),
  });
  const { data: routeCredentials = [], isLoading: loadingBindings } = useQuery({
    queryKey: ['admin', 'route-credentials', route.id],
    queryFn: () => adminConfigApi.listRouteCredentials(route.id),
  });

  const [overrides, setOverrides] = useState<Record<string, Partial<AdminRouteCredential>>>({});

  const rows = useMemo(() => {
    const bindingByCredential = new Map(routeCredentials.map((c) => [c.credentialId, c]));
    return providerCredentials
      .filter((c) => c.status !== 'deleted')
      .map((credential) => {
        const binding = bindingByCredential.get(credential.id);
        const override = overrides[credential.id] ?? {};
        return {
          credential,
          selected: override.enabled ?? binding?.enabled ?? false,
          priority: override.priority ?? binding?.priority ?? credential.priority,
          rpm: override.rpm !== undefined ? override.rpm : (binding?.rpm ?? null),
          tpm: override.tpm !== undefined ? override.tpm : (binding?.tpm ?? null),
        };
      });
  }, [overrides, providerCredentials, routeCredentials]);

  const saveMut = useMutation({
    mutationFn: () => {
      const selected = rows
        .filter((row) => row.selected)
        .map((row) => ({
          routeId: route.id,
          credentialId: row.credential.id,
          priority: row.priority,
          enabled: true,
          rpm: row.rpm,
          tpm: row.tpm,
        }));
      return adminConfigApi.setRouteCredentials(route.id, selected);
    },
    onSuccess: () => {
      toast.success('路由账号候选已保存（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials', route.id] });
      onClose();
    },
    onError: (e: unknown) => {
      toast.error(e instanceof Error ? e.message : '保存失败');
    },
  });

  const patchRow = (credentialId: string, patch: Partial<AdminRouteCredential>) => {
    setOverrides((current) => ({
      ...current,
      [credentialId]: { ...(current[credentialId] ?? {}), ...patch },
    }));
  };

  const loading = loadingCredentials || loadingBindings;

  return (
    <Modal
      open
      onClose={onClose}
      title="配置路由账号候选"
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => saveMut.mutate()}
          submitLabel="保存账号候选"
          submitting={saveMut.isPending}
          disabled={loading}
        />
      }
    >
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          <div className="font-mono text-foreground">{route.modelId}</div>
          <div className="mt-1">
            Provider <span className="font-mono">{route.providerId}</span> · 协议 <span className="font-mono">{route.protocol}</span> · 上游模型 <span className="font-mono">{route.upstreamModel}</span>
          </div>
          <div className="mt-1">可为同一个 Provider+协议路由绑定多个账号候选，并按账号设置优先级/RPM/TPM 覆盖；不选择任何账号则清空绑定并回退为 Provider 下全部 active 账号。</div>
        </div>

        {loading ? (
          <div className="py-10 text-center text-sm text-muted-foreground">加载账号…</div>
        ) : rows.length === 0 ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            当前 Provider 暂无账号，请先在“账号管理”中为 {route.providerId} 添加账号。
          </div>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <table className="min-w-full divide-y text-sm">
              <thead className="bg-muted/50 text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">启用</th>
                  <th className="px-3 py-2 text-left font-medium">账号</th>
                  <th className="px-3 py-2 text-left font-medium">优先级</th>
                  <th className="px-3 py-2 text-left font-medium">RPM</th>
                  <th className="px-3 py-2 text-left font-medium">TPM</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {rows.map((row) => (
                  <tr key={row.credential.id}>
                    <td className="px-3 py-2">
                      <input
                        type="checkbox"
                        checked={row.selected}
                        onChange={(e) => patchRow(row.credential.id, { enabled: e.target.checked })}
                        aria-label={`启用账号 ${row.credential.id}`}
                      />
                    </td>
                    <td className="px-3 py-2">
                      <div className="font-mono text-xs">{row.credential.id}</div>
                      <div className="text-xs text-muted-foreground">
                        {row.credential.keyPrefix || '****'}…{row.credential.keySuffix || '****'} · 默认优先级 {row.credential.priority}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <NumberField
                        value={String(row.priority)}
                        onChange={(v) => patchRow(row.credential.id, { priority: Number(v) || 0 })}
                        min={0}
                      />
                    </td>
                    <td className="px-3 py-2">
                      <NullableNumberInput
                        value={row.rpm}
                        onChange={(v) => patchRow(row.credential.id, { rpm: v })}
                        placeholder="继承"
                      />
                    </td>
                    <td className="px-3 py-2">
                      <NullableNumberInput
                        value={row.tpm}
                        onChange={(v) => patchRow(row.credential.id, { tpm: v })}
                        placeholder="继承"
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Modal>
  );
}

function NullableNumberInput({ value, onChange, placeholder }: { value: number | null; onChange: (v: number | null) => void; placeholder?: string }) {
  return (
    <NumberField
      value={value != null ? String(value) : ''}
      onChange={(v) => {
        const trimmed = v.trim();
        onChange(trimmed === '' ? null : (Number(trimmed) || null));
      }}
      min={1}
      placeholder={placeholder}
    />
  );
}

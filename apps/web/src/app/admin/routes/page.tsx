'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Plus } from 'lucide-react';
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
  const [protocolGroup, setProtocolGroup] = useState<RouteProviderGroup | null>(null);
  const [accountGroup, setAccountGroup] = useState<RouteProviderGroup | null>(null);
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

  const toggleProtocolMut = useMutation({
    mutationFn: async ({ group, protocol }: { group: RouteProviderGroup; protocol: string }) => {
      const existing = group.routes.find((r) => r.protocol === protocol);
      if (existing) {
        await adminConfigApi.updateRoute(existing.id, { enabled: !existing.enabled });
        return;
      }
      await adminConfigApi.createRoute({
        id: routeIDFor(group.modelId, group.providerId, protocol),
        modelId: group.modelId,
        providerId: group.providerId,
        upstreamModel: group.modelId,
        protocol,
        priority: 0,
        contextWindow: null,
        maxOutputTokens: null,
        rpm: null,
        tpm: null,
      });
    },
    onSuccess: () => {
      toast.success('协议开关已保存（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
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
              onEditProtocols={() => setProtocolGroup(group)}
              onConfigureAccounts={() => setAccountGroup(group)}
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
      {protocolGroup ? (
        <ProtocolToggleModal
          group={providerGroups.find((g) => g.key === protocolGroup.key) ?? protocolGroup}
          toggling={toggleProtocolMut.isPending}
          onToggleProtocol={(protocol) => toggleProtocolMut.mutate({ group: protocolGroup, protocol })}
          onClose={() => setProtocolGroup(null)}
        />
      ) : null}
      {accountGroup ? (
        <ProviderAccountsModal
          group={providerGroups.find((g) => g.key === accountGroup.key) ?? accountGroup}
          onSelectRoute={(route) => {
            setAccountGroup(null);
            setAccountRoute(route);
          }}
          onClose={() => setAccountGroup(null)}
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
  onEditProtocols,
  onConfigureAccounts,
}: {
  group: RouteProviderGroup;
  onEditProtocols: () => void;
  onConfigureAccounts: () => void;
}) {
  const enabledRoutes = group.routes.filter((r) => r.enabled && !r.quarantined);
  const disabledRoutes = group.routes.filter((r) => !r.enabled || r.quarantined);
  const enabledProtocols = enabledRoutes.map((r) => r.protocol).sort();
  return (
    <section className="rounded-lg border bg-card p-4 shadow-sm">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-sm font-semibold">{group.providerId}</h2>
            <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
              {group.modelId}
            </span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {enabledRoutes.length} 个启用协议 · {disabledRoutes.length} 个关闭/隔离协议
          </p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {enabledProtocols.length > 0 ? enabledProtocols.map((protocol) => (
              <span key={protocol} className="rounded-full bg-primary/10 px-2 py-0.5 font-mono text-[10px] text-primary">
                {protocol}
              </span>
            )) : (
              <span className="text-xs text-muted-foreground">暂无启用协议</span>
            )}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          <button
            type="button"
            onClick={onEditProtocols}
            className="inline-flex h-[var(--control-height-sm)] items-center rounded-sm border px-3 text-xs font-medium hover:bg-accent"
          >
            编辑协议
          </button>
          <button
            type="button"
            onClick={onConfigureAccounts}
            className="inline-flex h-[var(--control-height-sm)] items-center gap-1 rounded-sm border px-3 text-xs font-medium hover:bg-accent"
          >
            <KeyRound className="size-3.5" /> 配置账号
          </button>
        </div>
      </div>
    </section>
  );
}

function ProtocolToggleModal({
  group,
  toggling,
  onToggleProtocol,
  onClose,
}: {
  group: RouteProviderGroup;
  toggling?: boolean;
  onToggleProtocol: (protocol: string) => void;
  onClose: () => void;
}) {
  return (
    <Modal open onClose={onClose} title="编辑协议" maxWidth="md">
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          只控制协议是否可用；上游模型、Context、Max Output、RPM/TPM 继续沿用当前 route / Provider 默认配置。
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          {PROTOCOL_OPTIONS.map((protocol) => {
            const route = group.routes.find((r) => r.protocol === protocol);
            const active = !!route?.enabled && !route.quarantined;
            return (
              <button
                key={protocol}
                type="button"
                disabled={toggling}
                onClick={() => onToggleProtocol(protocol)}
                className={`flex items-center justify-between gap-3 rounded-lg border p-3 text-left transition-colors disabled:opacity-60 ${active ? 'border-primary/50 bg-primary/5' : 'bg-muted/20'}`}
              >
                <span>
                  <span className="block font-mono text-xs font-medium">{protocol}</span>
                  <span className="mt-1 block text-[11px] text-muted-foreground">
                    {route ? (active ? '已开启' : '已关闭') : '未创建，点击开启'}
                  </span>
                </span>
                <span className={`inline-flex h-5 w-9 items-center rounded-full p-0.5 transition-colors ${active ? 'bg-primary' : 'bg-muted-foreground/30'}`}>
                  <span className={`size-4 rounded-full bg-white transition-transform ${active ? 'translate-x-4' : ''}`} />
                </span>
              </button>
            );
          })}
        </div>
      </div>
    </Modal>
  );
}

function ProviderAccountsModal({
  group,
  onSelectRoute,
  onClose,
}: {
  group: RouteProviderGroup;
  onSelectRoute: (route: AdminRouteConfig) => void;
  onClose: () => void;
}) {
  const routes = group.routes
    .filter((r) => r.enabled && !r.quarantined)
    .sort((a, b) => a.protocol.localeCompare(b.protocol));
  return (
    <Modal open onClose={onClose} title="配置账号" maxWidth="md">
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          选择一个已启用协议后配置它的账号候选；未单独配置时会使用 Provider 下全部 active 账号。
        </div>
        {routes.length === 0 ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            当前 Provider 没有启用协议，请先在“编辑协议”中开启至少一个协议。
          </div>
        ) : (
          <div className="grid gap-2">
            {routes.map((route) => (
              <button
                key={route.id}
                type="button"
                onClick={() => onSelectRoute(route)}
                className="flex items-center justify-between rounded-lg border p-3 text-left hover:bg-accent"
              >
                <span>
                  <span className="block font-mono text-xs font-medium">{route.protocol}</span>
                  <span className="mt-1 block truncate text-[11px] text-muted-foreground">{route.upstreamModel}</span>
                </span>
                <span className="inline-flex items-center gap-1 text-xs text-primary">
                  <KeyRound className="size-3" /> 配置
                </span>
              </button>
            ))}
          </div>
        )}
      </div>
    </Modal>
  );
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

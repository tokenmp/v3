'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import { FilterChip } from '@/components/filter-chip';
import { Modal } from '@/components/ui/modal';
import {
  FormActions,
  NumberField,
  inputCls,
} from '@/components/ui/field';
import { PublishStatusHint } from '@/components/publish-status-hint';
import type { AdminRouteConfig, AdminRouteCredential } from '@/types/admin';

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

function logicalCredentialId(id: string): string {
  return id.replace(/-(openai|anthropic|responses)$/u, '');
}

export default function AdminRoutesPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [accountGroup, setAccountGroup] = useState<RouteProviderGroup | null>(null);
  const [strategyGroup, setStrategyGroup] = useState<RouteProviderGroup | null>(null);
  const [expandedGroupKey, setExpandedGroupKey] = useState<string | null>(null);

  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });
  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'model-configs'],
    queryFn: adminConfigApi.listModels,
  });

  const { data: credentials = [] } = useQuery({
    queryKey: ['admin', 'credentials', 'all'],
    queryFn: adminConfigApi.listAllCredentials,
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

  const credentialCountsByProvider = useMemo(() => {
    const grouped = new Map<string, Map<string, { active: boolean; total: number }>>();
    for (const credential of credentials) {
      const providerMap = grouped.get(credential.providerId) ?? new Map<string, { active: boolean; total: number }>();
      const logicalId = logicalCredentialId(credential.id);
      const current = providerMap.get(logicalId) ?? { active: false, total: 0 };
      current.total += 1;
      current.active = current.active || credential.status === 'active';
      providerMap.set(logicalId, current);
      grouped.set(credential.providerId, providerMap);
    }
    const counts = new Map<string, { active: number; total: number }>();
    for (const [providerId, providerMap] of grouped) {
      const accounts = Array.from(providerMap.values());
      counts.set(providerId, {
        active: accounts.filter((account) => account.active).length,
        total: accounts.length,
      });
    }
    return counts;
  }, [credentials]);

  const providerGroups = useMemo<RouteProviderGroup[]>(() => {
    const groups = new Map<string, RouteProviderGroup>();
    for (const route of filtered) {
      const key = selectedModel === 'all' ? route.providerId : `${route.modelId}::${route.providerId}`;
      const existing = groups.get(key);
      if (existing) {
        existing.routes.push(route);
      } else {
        groups.set(key, {
          key,
          modelId: selectedModel === 'all' ? 'all' : route.modelId,
          providerId: route.providerId,
          routes: [route],
        });
      }
    }
    return Array.from(groups.values()).sort((a, b) => a.providerId.localeCompare(b.providerId));
  }, [filtered, selectedModel]);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-semibold">路由管理</h1>
        <div className="ml-auto">
          <PublishStatusHint />
        </div>
      </div>

      {/* 工具栏：模型过滤 + 搜索 + 状态筛选 */}
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
          placeholder="搜索 Provider / 模型 / 上游模型"
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
      </div>

      <div className="rounded-lg border bg-card p-3 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">当前视图：</span>
        {selectedModel === 'all' ? '全部模型概览' : selectedModel} · {modelSummary.routes} 条路由 · {modelSummary.providers} 个 Provider · {modelSummary.protocols} 个能力协议。
        <span className="ml-1">未选择具体模型时展示 Provider 总览；选中模型后配置账号和策略。</span>
      </div>

      {/* Provider 分组列表 */}
      {isLoading ? (
        <div className="py-12 text-center text-sm text-muted-foreground">加载中…</div>
      ) : providerGroups.length === 0 ? (
        <div className="py-12 text-center text-sm text-muted-foreground">暂无路由配置</div>
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-xs text-muted-foreground">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Provider</th>
                <th className="px-3 py-2 text-left font-medium">范围</th>
                <th className="px-3 py-2 text-left font-medium">账号</th>
                <th className="px-3 py-2 text-left font-medium">能力</th>
                <th className="px-3 py-2 text-left font-medium">上游 / 路由</th>
                <th className="px-3 py-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {providerGroups.map((group) => (
                <ProviderRouteRow
                  key={group.key}
                  group={group}
                  selectedModel={selectedModel}
                  credentialCount={credentialCountsByProvider.get(group.providerId)}
                  expanded={expandedGroupKey === group.key}
                  onToggle={() => setExpandedGroupKey((current) => current === group.key ? null : group.key)}
                  onConfigureAccounts={() => setAccountGroup(group)}
                  onConfigureStrategy={() => setStrategyGroup(group)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {accountGroup ? (
        <GroupAccountsModal
          group={providerGroups.find((g) => g.key === accountGroup.key) ?? accountGroup}
          onClose={() => setAccountGroup(null)}
        />
      ) : null}
      {strategyGroup ? (
        <StrategyModal group={providerGroups.find((g) => g.key === strategyGroup.key) ?? strategyGroup} onClose={() => setStrategyGroup(null)} />
      ) : null}
    </div>
  );
}

function ProviderRouteRow({
  group,
  selectedModel,
  credentialCount,
  expanded,
  onToggle,
  onConfigureAccounts,
  onConfigureStrategy,
}: {
  group: RouteProviderGroup;
  selectedModel: string;
  credentialCount?: { active: number; total: number };
  expanded: boolean;
  onToggle: () => void;
  onConfigureAccounts: () => void;
  onConfigureStrategy: () => void;
}) {
  const enabledProtocols = Array.from(new Set(group.routes.filter((r) => r.enabled && !r.quarantined).map((r) => r.protocol))).sort();
  const coveredModels = Array.from(new Set(group.routes.map((r) => r.modelId))).sort();
  const upstreamModels = Array.from(new Set(group.routes.map((r) => r.upstreamModel).filter(Boolean))).sort();
  const isAllModels = selectedModel === 'all';
  const summary = isAllModels
    ? `${coveredModels.length} 个模型`
    : group.modelId;
  const upstreamSummary = isAllModels
    ? `${group.routes.length} 条路由`
    : (upstreamModels.length > 0 ? upstreamModels.join(' · ') : '未配置');

  return (
    <>
      <tr className="align-top hover:bg-muted/20">
        <td className="px-3 py-2">
          <button type="button" onClick={onToggle} className="font-mono text-sm font-medium text-primary hover:underline">
            {group.providerId}
          </button>
        </td>
        <td className="px-3 py-2 text-xs text-muted-foreground">{summary}</td>
        <td className="px-3 py-2 text-xs text-muted-foreground">{credentialCount ? `${credentialCount.active} / ${credentialCount.total}` : '—'}</td>
        <td className="px-3 py-2">
          <div className="flex flex-wrap gap-1">
            {enabledProtocols.length > 0 ? enabledProtocols.map((protocol) => (
              <span key={protocol} className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                {protocolLabel(protocol)}
              </span>
            )) : <span className="text-xs text-muted-foreground">暂无</span>}
          </div>
        </td>
        <td className="max-w-[320px] px-3 py-2 text-xs text-muted-foreground">
          <span className="line-clamp-2">{upstreamSummary}</span>
        </td>
        <td className="px-3 py-2 text-right">
          <div className="inline-flex items-center gap-2">
            <button type="button" onClick={onToggle} className="rounded-sm border px-2 py-1 text-xs hover:bg-accent">
              {expanded ? '收起' : '查看'}
            </button>
            {isAllModels ? null : (
              <>
                <button type="button" onClick={onConfigureAccounts} className="rounded-sm border px-2 py-1 text-xs hover:bg-accent">账号</button>
                <button type="button" onClick={onConfigureStrategy} className="rounded-sm border px-2 py-1 text-xs hover:bg-accent">策略</button>
              </>
            )}
          </div>
        </td>
      </tr>
      {expanded ? (
        <tr className="bg-muted/10">
          <td colSpan={6} className="px-3 py-3 text-xs text-muted-foreground">
            <div className="grid gap-2 sm:grid-cols-3">
              <div><span className="text-foreground">覆盖模型：</span>{coveredModels.slice(0, 10).join(' · ')}{coveredModels.length > 10 ? ` 等 ${coveredModels.length} 个` : ''}</div>
              <div><span className="text-foreground">能力：</span>{enabledProtocols.length > 0 ? enabledProtocols.map(protocolLabel).join(' · ') : '暂无'}</div>
              <div><span className="text-foreground">路由：</span>{group.routes.length} 条</div>
              {!isAllModels ? <div className="sm:col-span-3"><span className="text-foreground">上游模型：</span>{upstreamModels.length > 0 ? upstreamModels.join(' · ') : '未配置'}</div> : null}
            </div>
          </td>
        </tr>
      ) : null}
    </>
  );
}


function protocolLabel(protocol: string): string {
  switch (protocol) {
    case 'openai_chat':
      return 'OpenAI Chat';
    case 'openai_responses':
      return 'Responses';
    case 'anthropic_messages':
      return 'Anthropic';
    case 'openai_images':
      return 'Images';
    default:
      return protocol;
  }
}

function StrategyModal({ group, onClose }: { group: RouteProviderGroup; onClose: () => void }) {
  const enabledProtocols = Array.from(new Set(group.routes.filter((r) => r.enabled && !r.quarantined).map((r) => r.protocol))).sort();
  const priorities = group.routes.map((r) => r.priority);
  const minPriority = priorities.length > 0 ? Math.min(...priorities) : 0;
  const rpmOverrides = group.routes.filter((r) => r.rpm != null).length;
  const tpmOverrides = group.routes.filter((r) => r.tpm != null).length;
  return (
    <Modal open onClose={onClose} title="策略" maxWidth="md">
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          当前页面只展示和确认该 Provider 组使用的路由策略来源；全局 softmax 权重在“重试策略”页维护。
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border bg-card p-3">
            <div className="text-xs text-muted-foreground">选号策略</div>
            <div className="mt-1 text-sm font-medium">继承全局策略</div>
            <div className="mt-1 text-xs text-muted-foreground">priority / softmax 由全局配置决定</div>
          </div>
          <div className="rounded-lg border bg-card p-3">
            <div className="text-xs text-muted-foreground">启用能力</div>
            <div className="mt-1 text-sm font-medium">{enabledProtocols.length > 0 ? enabledProtocols.map(protocolLabel).join(' · ') : '暂无'}</div>
            <div className="mt-1 text-xs text-muted-foreground">协议能力来自 Provider endpoints</div>
          </div>
          <div className="rounded-lg border bg-card p-3">
            <div className="text-xs text-muted-foreground">优先级</div>
            <div className="mt-1 text-sm font-medium">当前最小 priority：{minPriority}</div>
            <div className="mt-1 text-xs text-muted-foreground">数字越小越优先</div>
          </div>
          <div className="rounded-lg border bg-card p-3">
            <div className="text-xs text-muted-foreground">容量覆盖</div>
            <div className="mt-1 text-sm font-medium">RPM {rpmOverrides} 项 · TPM {tpmOverrides} 项</div>
            <div className="mt-1 text-xs text-muted-foreground">未覆盖时继承 Provider/账号默认值</div>
          </div>
        </div>
      </div>
    </Modal>
  );
}


function GroupAccountsModal({ group, onClose }: { group: RouteProviderGroup; onClose: () => void }) {
  const queryClient = useQueryClient();
  const enabledRoutes = useMemo(
    () => group.routes.filter((r) => r.enabled && !r.quarantined),
    [group.routes],
  );
  const { data: providerCredentials = [], isLoading: loadingCredentials } = useQuery({
    queryKey: ['admin', 'provider-credentials', group.providerId],
    queryFn: () => adminConfigApi.listCredentials(group.providerId),
  });
  const { data: routeCredentialSets = [], isLoading: loadingBindings } = useQuery({
    queryKey: ['admin', 'route-credentials-group', group.key, enabledRoutes.map((r) => r.id).join('|')],
    queryFn: async () => Promise.all(enabledRoutes.map((route) => adminConfigApi.listRouteCredentials(route.id))),
    enabled: enabledRoutes.length > 0,
  });

  const [overrides, setOverrides] = useState<Record<string, Partial<AdminRouteCredential>>>({});

  const rows = useMemo(() => {
    const visibleCredentials = providerCredentials.filter((c) => c.status !== 'deleted');
    const anyExplicitBinding = routeCredentialSets.some((set) => set.length > 0);
    const bindingByCredential = new Map<string, AdminRouteCredential>();
    for (const set of routeCredentialSets) {
      for (const binding of set) {
        if (!bindingByCredential.has(binding.credentialId)) bindingByCredential.set(binding.credentialId, binding);
      }
    }
    const groupedCredentials = new Map<string, typeof visibleCredentials>();
    for (const credential of visibleCredentials) {
      const logicalId = logicalCredentialId(credential.id);
      groupedCredentials.set(logicalId, [...(groupedCredentials.get(logicalId) ?? []), credential]);
    }
    return Array.from(groupedCredentials.entries()).map(([logicalId, accountCredentials]) => {
      const primary = (accountCredentials.find((credential) => credential.status === 'active') ?? accountCredentials[0])!;
      const bindings = accountCredentials.map((credential) => bindingByCredential.get(credential.id)).filter((binding): binding is AdminRouteCredential => Boolean(binding));
      const binding = bindings[0];
      const override = overrides[logicalId] ?? {};
      return {
        logicalId,
        credentialIds: accountCredentials.map((credential) => credential.id),
        protocols: accountCredentials.map((credential) => credential.id.match(/-(openai|anthropic|responses)$/u)?.[1]).filter((value): value is string => Boolean(value)).sort(),
        credential: primary,
        selected: override.enabled ?? (bindings.length > 0 ? bindings.some((item) => item.enabled) : !anyExplicitBinding),
        priority: override.priority ?? binding?.priority ?? primary.priority,
        rpm: override.rpm !== undefined ? override.rpm : (binding?.rpm ?? null),
        tpm: override.tpm !== undefined ? override.tpm : (binding?.tpm ?? null),
      };
    }).sort((a, b) => a.logicalId.localeCompare(b.logicalId));
  }, [overrides, providerCredentials, routeCredentialSets]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const selected = rows
        .filter((row) => row.selected)
        .flatMap((row) => row.credentialIds.map((credentialId) => ({
          credentialId,
          priority: row.priority,
          enabled: true,
          rpm: row.rpm,
          tpm: row.tpm,
        })));
      await Promise.all(enabledRoutes.map((route) => adminConfigApi.setRouteCredentials(
        route.id,
        selected.map((item) => ({
          routeId: route.id,
          credentialId: item.credentialId,
          priority: item.priority,
          enabled: item.enabled,
          rpm: item.rpm,
          tpm: item.tpm,
        })),
      )));
    },
    onSuccess: () => {
      toast.success('账号候选已同步到该 Provider 组的所有启用协议（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials-group'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const patchRow = (logicalId: string, patch: Partial<AdminRouteCredential>) => {
    setOverrides((current) => ({
      ...current,
      [logicalId]: { ...(current[logicalId] ?? {}), ...patch },
    }));
  };

  const loading = loadingCredentials || loadingBindings;

  return (
    <Modal
      open
      onClose={onClose}
      title="配置账号"
      maxWidth="lg"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => saveMut.mutate()}
          submitLabel="保存账号候选"
          submitting={saveMut.isPending}
          disabled={loading || enabledRoutes.length === 0}
        />
      }
    >
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          这里统一配置该 Provider 组的账号候选；保存后会同步到此组下所有已启用协议 route。协议只是上游请求适配维度，不需要在账号配置里单独选择。
        </div>
        {enabledRoutes.length === 0 ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            当前 Provider 没有启用协议，请先在“编辑协议”中开启至少一个协议。
          </div>
        ) : loading ? (
          <div className="py-10 text-center text-sm text-muted-foreground">加载账号…</div>
        ) : rows.length === 0 ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            当前 Provider 暂无账号，请先在“账号管理”中为 {group.providerId} 添加账号。
          </div>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">启用</th>
                  <th className="px-3 py-2 text-left font-medium">账号</th>
                  <th className="px-3 py-2 text-left font-medium">能力行</th>
                  <th className="px-3 py-2 text-left font-medium">优先级</th>
                  <th className="px-3 py-2 text-left font-medium">RPM</th>
                  <th className="px-3 py-2 text-left font-medium">TPM</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {rows.map((row) => (
                  <tr key={row.logicalId} className={row.selected ? 'bg-primary/5' : undefined}>
                    <td className="px-3 py-2">
                      <input
                        type="checkbox"
                        checked={row.selected}
                        onChange={(e) => patchRow(row.logicalId, { enabled: e.target.checked })}
                        aria-label={`启用账号 ${row.logicalId}`}
                      />
                    </td>
                    <td className="min-w-[260px] px-3 py-2">
                      <div className="truncate font-mono text-xs font-medium" title={row.logicalId}>{row.logicalId}</div>
                      <div className="mt-0.5 text-xs text-muted-foreground">
                        {row.credential.keyPrefix || '****'}…{row.credential.keySuffix || '****'} · 默认 {row.credential.priority}
                      </div>
                    </td>
                    <td className="px-3 py-2 text-xs text-muted-foreground">
                      {row.credentialIds.length > 1 ? `${row.credentialIds.length} 条` : '1 条'}
                      {row.protocols.length > 0 ? ` · ${row.protocols.join(' / ')}` : ''}
                    </td>
                    <td className="w-28 px-3 py-2">
                      <NumberField
                        value={String(row.priority)}
                        onChange={(v) => patchRow(row.logicalId, { priority: Number(v) || 0 })}
                        min={0}
                      />
                    </td>
                    <td className="w-28 px-3 py-2">
                      <NullableNumberInput
                        value={row.rpm}
                        onChange={(v) => patchRow(row.logicalId, { rpm: v })}
                        placeholder="继承"
                      />
                    </td>
                    <td className="w-28 px-3 py-2">
                      <NullableNumberInput
                        value={row.tpm}
                        onChange={(v) => patchRow(row.logicalId, { tpm: v })}
                        placeholder="继承"
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>        )}
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

'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound } from 'lucide-react';
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

export default function AdminRoutesPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [accountGroup, setAccountGroup] = useState<RouteProviderGroup | null>(null);
  const [strategyGroup, setStrategyGroup] = useState<RouteProviderGroup | null>(null);

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
    const counts = new Map<string, { active: number; total: number }>();
    for (const credential of credentials) {
      const current = counts.get(credential.providerId) ?? { active: 0, total: 0 };
      current.total += 1;
      if (credential.status === 'active') current.active += 1;
      counts.set(credential.providerId, current);
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
        <div className="grid gap-3">
          {providerGroups.map((group) => (
            <ProviderRouteCard
              key={group.key}
              group={group}
              selectedModel={selectedModel}
              credentialCount={credentialCountsByProvider.get(group.providerId)}
              onConfigureAccounts={() => setAccountGroup(group)}
              onConfigureStrategy={() => setStrategyGroup(group)}
            />
          ))}
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

function ProviderRouteCard({
  group,
  selectedModel,
  credentialCount,
  onConfigureAccounts,
  onConfigureStrategy,
}: {
  group: RouteProviderGroup;
  selectedModel: string;
  credentialCount?: { active: number; total: number };
  onConfigureAccounts: () => void;
  onConfigureStrategy: () => void;
}) {
  const enabledProtocols = Array.from(new Set(group.routes.filter((r) => r.enabled && !r.quarantined).map((r) => r.protocol))).sort();
  const coveredModels = Array.from(new Set(group.routes.map((r) => r.modelId))).sort();
  const upstreamModels = Array.from(new Set(group.routes.map((r) => r.upstreamModel).filter(Boolean))).sort();
  const isAllModels = selectedModel === 'all';
  return (
    <section className="rounded-lg border bg-card p-4 shadow-sm">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-sm font-semibold">{group.providerId}</h2>
            <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
              {isAllModels ? `${coveredModels.length} 个模型` : group.modelId}
            </span>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span>账号：{credentialCount ? `${credentialCount.active} / ${credentialCount.total}` : '—'}</span>
            {!isAllModels && upstreamModels.length === 1 ? <span>上游：{upstreamModels[0]}</span> : null}
          </div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {enabledProtocols.length > 0 ? enabledProtocols.map((protocol) => (
              <span key={protocol} className="rounded-full bg-primary/10 px-2 py-0.5 font-mono text-[10px] text-primary">
                {protocolLabel(protocol)}
              </span>
            )) : (
              <span className="text-xs text-muted-foreground">暂无启用能力</span>
            )}
          </div>
        </div>
        {isAllModels ? (
          <div className="flex shrink-0 flex-wrap gap-2">
            <button
              type="button"
              className="inline-flex h-[var(--control-height-sm)] items-center rounded-sm border px-3 text-xs font-medium hover:bg-accent"
              onClick={() => { window.location.href = '/admin/providers'; }}
            >
              Provider 详情
            </button>
          </div>
        ) : (
          <div className="flex shrink-0 flex-wrap gap-2">
            <button
              type="button"
              onClick={onConfigureAccounts}
              className="inline-flex h-[var(--control-height-sm)] items-center gap-1 rounded-sm bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
            >
              <KeyRound className="size-3.5" /> 配置账号
            </button>
            <button
              type="button"
              onClick={onConfigureStrategy}
              className="inline-flex h-[var(--control-height-sm)] items-center rounded-sm border px-3 text-xs font-medium hover:bg-accent"
            >
              策略
            </button>
          </div>
        )}
      </div>
    </section>
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
  return (
    <Modal open onClose={onClose} title="路由策略" maxWidth="md">
      <div className="space-y-4">
        <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          模型 <span className="font-mono text-foreground">{group.modelId}</span> · Provider <span className="font-mono text-foreground">{group.providerId}</span>。
          这里后续用于配置该模型在该 Provider 下的选号策略、优先级和容量覆盖；当前先保留入口，实际 softmax/enforcement 仍在后续批次接入。
        </div>
        <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
          策略配置表单待接入。当前路由继续继承全局策略和已有 route/provider 配置。
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
    const activeCredentials = providerCredentials.filter((c) => c.status !== 'deleted');
    const anyExplicitBinding = routeCredentialSets.some((set) => set.length > 0);
    const bindingByCredential = new Map<string, AdminRouteCredential>();
    for (const set of routeCredentialSets) {
      for (const binding of set) {
        if (!bindingByCredential.has(binding.credentialId)) bindingByCredential.set(binding.credentialId, binding);
      }
    }
    return activeCredentials.map((credential) => {
      const binding = bindingByCredential.get(credential.id);
      const override = overrides[credential.id] ?? {};
      return {
        credential,
        selected: override.enabled ?? binding?.enabled ?? !anyExplicitBinding,
        priority: override.priority ?? binding?.priority ?? credential.priority,
        rpm: override.rpm !== undefined ? override.rpm : (binding?.rpm ?? null),
        tpm: override.tpm !== undefined ? override.tpm : (binding?.tpm ?? null),
      };
    });
  }, [overrides, providerCredentials, routeCredentialSets]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const selected = rows
        .filter((row) => row.selected)
        .map((row) => ({
          credentialId: row.credential.id,
          priority: row.priority,
          enabled: true,
          rpm: row.rpm,
          tpm: row.tpm,
        }));
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

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

export default function AdminRoutesPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [protocolGroup, setProtocolGroup] = useState<RouteProviderGroup | null>(null);
  const [accountGroup, setAccountGroup] = useState<RouteProviderGroup | null>(null);

  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });
  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'model-configs'],
    queryFn: adminConfigApi.listModels,
  });

  const toggleProtocolMut = useMutation({
    mutationFn: async ({ group, protocol }: { group: RouteProviderGroup; protocol: string }) => {
      const existingRoutes = group.routes.filter((r) => r.protocol === protocol);
      if (existingRoutes.length > 0) {
        const anyActive = existingRoutes.some((r) => r.enabled && !r.quarantined);
        await Promise.all(existingRoutes.map((route) => adminConfigApi.updateRoute(route.id, { enabled: !anyActive })));
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

      {protocolGroup ? (
        <ProtocolToggleModal
          group={providerGroups.find((g) => g.key === protocolGroup.key) ?? protocolGroup}
          toggling={toggleProtocolMut.isPending}
          onToggleProtocol={(protocol) => toggleProtocolMut.mutate({ group: protocolGroup, protocol })}
          onClose={() => setProtocolGroup(null)}
        />
      ) : null}
      {accountGroup ? (
        <GroupAccountsModal
          group={providerGroups.find((g) => g.key === accountGroup.key) ?? accountGroup}
          onClose={() => setAccountGroup(null)}
        />
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
  const enabledProtocols = Array.from(new Set(group.routes.filter((r) => r.enabled && !r.quarantined).map((r) => r.protocol))).sort();
  const disabledProtocols = PROTOCOL_OPTIONS.filter((protocol) => !enabledProtocols.includes(protocol) && group.routes.some((r) => r.protocol === protocol));
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
            {enabledProtocols.length} 个启用协议 · {disabledProtocols.length} 个关闭/隔离协议
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
            const protocolRoutes = group.routes.filter((r) => r.protocol === protocol);
            const active = protocolRoutes.some((r) => r.enabled && !r.quarantined);
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
                    {protocolRoutes.length > 0 ? (active ? '已开启' : '已关闭') : '未创建，点击开启'}
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


function routeIDFor(modelId: string, providerId: string, protocol: string): string {
  const safe = `${modelId}-${providerId}-${protocol}`
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return `route-${safe}`.slice(0, 120);
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

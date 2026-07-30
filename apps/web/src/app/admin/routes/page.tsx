'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { adminConfigApi } from '@/lib/api/admin';
import { FilterChip } from '@/components/filter-chip';
import { PageHeader } from '@/components/page-header';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Modal } from '@/components/ui/modal';
import { Sheet } from '@/components/ui/sheet';
import { Switch } from '@/components/ui/switch';
import {
  Field,
  FormActions,
  FormSection,
  SelectField,
  TextField,
  inputCls,
} from '@/components/ui/field';
import { PublishStatusHint } from '@/components/publish-status-hint';
import type { AdminProvider, AdminRouteConfig, AdminRouteCredential, AdminUpstreamCredential } from '@/types/admin';

type StatusFilter = 'all' | 'active' | 'disabled';

type RouteProviderGroup = {
  key: string;
  modelId: string;
  providerId: string;
  routes: AdminRouteConfig[];
};

type RouteModelGroup = {
  modelId: string;
  routes: AdminRouteConfig[];
  providers: string[];
  protocols: string[];
  upstreamModels: string[];
  accountCount: number;
  activeAccountCount: number;
};

const PROTOCOL_OPTIONS = ['openai_chat', 'anthropic_messages', 'openai_responses', 'openai_images'];

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '正常' },
  { key: 'disabled', label: '已禁用' },
];

function logicalCredentialId(id: string): string {
  return id.replace(/-(openai|anthropic|responses)$/u, '');
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

function providerGroupsForModel(group: RouteModelGroup): RouteProviderGroup[] {
  const groups = new Map<string, RouteProviderGroup>();
  for (const route of group.routes) {
    const key = `${group.modelId}::${route.providerId}`;
    const existing = groups.get(key);
    if (existing) {
      existing.routes.push(route);
    } else {
      groups.set(key, { key, modelId: group.modelId, providerId: route.providerId, routes: [route] });
    }
  }
  return Array.from(groups.values()).sort((a, b) => a.providerId.localeCompare(b.providerId));
}

function routeIdFor(modelId: string, providerId: string, protocol: string, upstreamModel: string): string {
  const safe = (value: string) => value.trim().toLowerCase().replace(/[^a-z0-9._-]+/gu, '-').replace(/^-+|-+$/gu, '') || 'route';
  return `${safe(modelId)}-${safe(providerId)}-${safe(protocol)}-${safe(upstreamModel)}`.slice(0, 180);
}

export default function AdminRoutesPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [pickerGroup, setPickerGroup] = useState<RouteProviderGroup | null>(null);
  const [pickerModelGroup, setPickerModelGroup] = useState<RouteModelGroup | null>(null);
  const [routeModelGroup, setRouteModelGroup] = useState<RouteModelGroup | null>(null);

  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });
  const { data: providers = [] } = useQuery({
    queryKey: ['admin', 'providers'],
    queryFn: adminConfigApi.listProviders,
  });
  const { data: credentials = [] } = useQuery({
    queryKey: ['admin', 'credentials', 'all'],
    queryFn: adminConfigApi.listAllCredentials,
  });

  const routeIdsKey = useMemo(() => routes.map((route) => route.id).sort().join('|'), [routes]);
  const { data: allRouteCredentialSets = [] } = useQuery({
    queryKey: ['admin', 'route-credentials', 'all-routes', routeIdsKey],
    queryFn: async () => Promise.all(routes.map((route) => adminConfigApi.listRouteCredentials(route.id))),
    enabled: routes.length > 0,
  });

  const routeModelOptions = useMemo(() => {
    const ids = new Set<string>();
    routes.forEach((r) => ids.add(r.modelId));
    return Array.from(ids).sort((a, b) => a.localeCompare(b));
  }, [routes]);

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

  const routeStats = useMemo(() => ({
    models: new Set(routes.map((route) => route.modelId)).size,
    routes: routes.length,
    providers: new Set(routes.map((route) => route.providerId)).size,
    protocols: new Set(routes.map((route) => route.protocol)).size,
    quarantined: routes.filter((route) => route.quarantined).length,
  }), [routes]);

  const routeCredentialsByRouteId = useMemo(() => {
    const map = new Map<string, AdminRouteCredential[]>();
    routes.forEach((route, index) => map.set(route.id, allRouteCredentialSets[index] ?? []));
    return map;
  }, [allRouteCredentialSets, routes]);

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

  const modelGroups = useMemo<RouteModelGroup[]>(() => {
    const groups = new Map<string, AdminRouteConfig[]>();
    const credentialStatus = new Map(credentials.map((credential) => [credential.id, credential.status]));
    for (const route of filtered) {
      groups.set(route.modelId, [...(groups.get(route.modelId) ?? []), route]);
    }
    return Array.from(groups.entries()).map(([modelId, groupRoutes]) => {
      const accountIds = new Set<string>();
      const activeAccountIds = new Set<string>();
      for (const route of groupRoutes) {
        for (const binding of routeCredentialsByRouteId.get(route.id) ?? []) {
          if (!binding.enabled) continue;
          const logicalId = logicalCredentialId(binding.credentialId);
          accountIds.add(logicalId);
          if (credentialStatus.get(binding.credentialId) === 'active') activeAccountIds.add(logicalId);
        }
      }
      return {
        modelId,
        routes: groupRoutes,
        providers: Array.from(new Set(groupRoutes.map((route) => route.providerId))).sort(),
        protocols: Array.from(new Set(groupRoutes.map((route) => route.protocol))).sort(),
        upstreamModels: Array.from(new Set(groupRoutes.map((route) => route.upstreamModel).filter(Boolean))).sort(),
        accountCount: accountIds.size,
        activeAccountCount: activeAccountIds.size,
      };
    }).sort((a, b) => a.modelId.localeCompare(b.modelId));
  }, [credentials, filtered, routeCredentialsByRouteId]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="路由配置"
        description="配置用户请求如何转发到 Provider、协议与上游账号"
        actions={<PublishStatusHint />}
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-5">
        {[
          ['模型数', routeStats.models],
          ['路由数', routeStats.routes],
          ['Provider 数', routeStats.providers],
          ['协议数', routeStats.protocols],
          ['异常数', routeStats.quarantined],
        ].map(([label, value]) => (
          <div key={label} className="rounded-lg border bg-card p-3">
            <div className="text-xs text-muted-foreground">{label}</div>
            <div className="mt-1 text-xl font-semibold tabular-nums">{value}</div>
          </div>
        ))}
      </div>

      <div className="flex flex-col gap-3 rounded-xl border bg-card p-4 sm:flex-row sm:flex-wrap sm:items-center">
        <select
          value={selectedModel}
          onChange={(e) => setSelectedModel(e.target.value)}
          className={`${inputCls} w-full font-mono sm:w-72`}
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
          className={`${inputCls} w-full max-w-xs`}
        />
        <div className="flex flex-wrap gap-1.5 sm:ml-auto">
          {STATUS_FILTERS.map((filter) => (
            <FilterChip
              key={filter.key}
              label={filter.label}
              active={statusFilter === filter.key}
              onClick={() => setStatusFilter(filter.key)}
            />
          ))}
        </div>
      </div>

      {isLoading ? (
        <div className="rounded-xl border border-dashed py-16 text-center text-sm text-muted-foreground">加载中…</div>
      ) : modelGroups.length === 0 ? (
        <div className="rounded-xl border border-dashed py-16 text-center text-sm text-muted-foreground">暂无路由配置</div>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {modelGroups.map((group) => (
            <ModelRouteCard
              key={group.modelId}
              group={group}
              credentialCountsByProvider={credentialCountsByProvider}
              onSelectAccounts={() => setPickerModelGroup(group)}
              onConfigureRoutes={() => setRouteModelGroup(group)}
            />
          ))}
        </div>
      )}

      {pickerModelGroup ? (
        <AccountPickerModal
          title="选择账号"
          description={pickerModelGroup.modelId}
          hint={`为 ${pickerModelGroup.modelId} 选择上游账号。账号按 Provider 分组展示，保存后同步到该模型下对应 Provider 的全部启用能力。`}
          providerGroups={providerGroupsForModel(pickerModelGroup)}
          credentials={credentials}
          onClose={() => setPickerModelGroup(null)}
        />
      ) : null}
      {pickerGroup ? (
        <AccountPickerModal
          title="配置账号"
          description={`${pickerGroup.modelId} · ${pickerGroup.providerId}`}
          hint={`为 ${pickerGroup.modelId} 选择可参与路由的 ${pickerGroup.providerId} 上游账号。保存后会同步到当前 Provider 组的全部启用能力。`}
          providerGroups={[pickerGroup]}
          credentials={credentials}
          onClose={() => setPickerGroup(null)}
        />
      ) : null}
      {routeModelGroup ? (
        <RouteConfigSheet
          group={routeModelGroup}
          providers={providers}
          credentials={credentials}
          onClose={() => setRouteModelGroup(null)}
        />
      ) : null}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 模型路由卡片（始终按模型分组，单模型视图也是同结构）
// ---------------------------------------------------------------------------

function ModelRouteCard({
  group,
  credentialCountsByProvider,
  onSelectAccounts,
  onConfigureRoutes,
}: {
  group: RouteModelGroup;
  credentialCountsByProvider: Map<string, { active: number; total: number }>;
  onSelectAccounts: () => void;
  onConfigureRoutes: () => void;
}) {
  const providerEntries = group.providers.map((providerId) => {
    const count = credentialCountsByProvider.get(providerId);
    return { providerId, active: count?.active ?? 0, total: count?.total ?? 0 };
  });
  return (
    <section className="flex flex-col gap-3 rounded-xl border bg-card p-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h2 className="break-all font-mono font-medium">{group.modelId}</h2>
          <p className="mt-1 text-xs text-muted-foreground">{group.providers.length} 个 Provider · {group.routes.length} 条路由</p>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2 text-xs text-muted-foreground">
          <span>账号 {group.activeAccountCount}/{group.accountCount}</span>
        </div>
      </div>

      <div className="space-y-2 border-y py-3">
        <div className="flex flex-wrap gap-1.5">
          {group.protocols.map((protocol) => (
            <Badge key={protocol} variant="secondary">{protocolLabel(protocol)}</Badge>
          ))}
        </div>
        <p className="line-clamp-2 text-sm text-muted-foreground">
          {group.upstreamModels.length > 0 ? group.upstreamModels.join(' · ') : '未配置上游模型'}
        </p>
      </div>

      <div className="space-y-1">
        {providerEntries.map(({ providerId, active, total }) => (
          <div key={providerId} className="flex items-center justify-between rounded-md bg-muted/30 px-3 py-2 text-xs">
            <span className="font-mono text-muted-foreground">{providerId}</span>
            <span className="tabular-nums text-muted-foreground">账号 {active}/{total}</span>
          </div>
        ))}
      </div>

      <div className="mt-auto flex flex-wrap gap-2 pt-1">
        <Button type="button" variant="outline" size="sm" onClick={onSelectAccounts}>选择账号</Button>
        <Button type="button" variant="outline" size="sm" onClick={onConfigureRoutes}>配置路由</Button>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// 统一的账号选择弹窗（去 checkbox，点卡切换；带搜索/过滤）
// ---------------------------------------------------------------------------

function AccountPickerModal({
  title,
  description,
  hint,
  providerGroups,
  credentials,
  onClose,
}: {
  title: string;
  description: string;
  hint: string;
  providerGroups: RouteProviderGroup[];
  credentials: AdminUpstreamCredential[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const enabledRoutesByProvider = useMemo(() => {
    const map = new Map<string, AdminRouteConfig[]>();
    for (const pg of providerGroups) {
      map.set(pg.providerId, pg.routes.filter((r) => r.enabled && !r.quarantined));
    }
    return map;
  }, [providerGroups]);

  const enabledRouteIds = useMemo(
    () => providerGroups.flatMap((pg) => enabledRoutesByProvider.get(pg.providerId) ?? []).map((r) => r.id),
    [providerGroups, enabledRoutesByProvider],
  );
  const enabledRouteIdsKey = enabledRouteIds.slice().sort().join('|');

  const { data: routeCredentialSets = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-credentials-picker', providerGroups.map((g) => g.key).join('::'), enabledRouteIdsKey],
    queryFn: async () => Promise.all(
      providerGroups.flatMap((pg) =>
        (enabledRoutesByProvider.get(pg.providerId) ?? []).map((route) => adminConfigApi.listRouteCredentials(route.id)),
      ),
    ),
    enabled: enabledRouteIds.length > 0,
  });

  const [overrides, setOverrides] = useState<Record<string, boolean>>({});
  const [accountSearch, setAccountSearch] = useState('');
  const [availableOnly, setAvailableOnly] = useState(false);
  const [selectedOnly, setSelectedOnly] = useState(false);

  const rows = useMemo(() => {
    const bindingIds = new Set<string>();
    for (const set of routeCredentialSets) {
      for (const binding of set) {
        if (binding.enabled) bindingIds.add(binding.credentialId);
      }
    }
    const anyExplicitBinding = routeCredentialSets.some((set) => set.length > 0);
    let counter = 0;
    return providerGroups.flatMap((providerGroup) => {
      const providerCredentials = credentials.filter(
        (credential) => credential.providerId === providerGroup.providerId && credential.status !== 'deleted',
      );
      const grouped = new Map<string, AdminUpstreamCredential[]>();
      for (const credential of providerCredentials) {
        const logicalId = logicalCredentialId(credential.id);
        grouped.set(logicalId, [...(grouped.get(logicalId) ?? []), credential]);
      }
      return Array.from(grouped.entries()).map(([logicalId, accountCredentials]) => {
        const primary = (accountCredentials.find((credential) => credential.status === 'active') ?? accountCredentials[0])!;
        const selectedByBindings = accountCredentials.some((credential) => bindingIds.has(credential.id));
        const overrideKey = `${providerGroup.providerId}::${logicalId}`;
        const index = counter++;
        const defaultSelected = anyExplicitBinding ? selectedByBindings : primary.status === 'active';
        const dirty = overrideKey in overrides;
        return {
          overrideKey,
          providerGroup,
          logicalId,
          credentialIds: accountCredentials.map((credential) => credential.id),
          credential: primary,
          index,
          defaultSelected,
          dirty,
          selected: overrides[overrideKey] ?? defaultSelected,
        };
      });
    });
  }, [credentials, overrides, providerGroups, routeCredentialSets]);

  const saveMut = useMutation({
    mutationFn: async () => {
      await Promise.all(providerGroups.map(async (providerGroup) => {
        const providerRows = rows.filter((row) => row.providerGroup.providerId === providerGroup.providerId && row.selected);
        const selected = providerRows.flatMap((row) => row.credentialIds.map((credentialId) => ({
          credentialId,
          priority: row.credential.priority,
          enabled: true,
          rpm: null,
          tpm: null,
        })));
        const routesToUpdate = enabledRoutesByProvider.get(providerGroup.providerId) ?? [];
        await Promise.all(routesToUpdate.map((route) => adminConfigApi.setRouteCredentials(
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
      }));
    },
    onSuccess: () => {
      toast.success('账号选择已保存（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials-picker'] });
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const selectedCount = rows.filter((row) => row.selected).length;
  const addedCount = rows.filter((row) => row.dirty && row.selected && !row.defaultSelected).length;
  const removedCount = rows.filter((row) => row.dirty && !row.selected && row.defaultSelected).length;
  const hasDirty = addedCount > 0 || removedCount > 0;
  const visibleRows = rows.filter((row) => {
    const query = accountSearch.trim().toLowerCase();
    if (availableOnly && row.credential.status !== 'active') return false;
    if (selectedOnly && !row.selected) return false;
    if (!query) return true;
    return [row.providerGroup.providerId, row.logicalId, row.credential.keyPrefix, row.credential.keySuffix]
      .some((value) => value?.toLowerCase().includes(query));
  });

  const noEnabledRoutes = enabledRouteIds.length === 0;

  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      description={description}
      maxWidth="2xl"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => saveMut.mutate()}
          submitLabel="保存账号选择"
          submitting={saveMut.isPending}
          disabled={isLoading || noEnabledRoutes}
        />
      }
    >
      <div className="space-y-5">
        <p className="rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground">{hint}</p>
        {noEnabledRoutes ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">当前没有启用路由，请先配置并启用至少一条路由。</div>
        ) : isLoading ? (
          <div className="py-10 text-center text-sm text-muted-foreground">加载账号…</div>
        ) : rows.length === 0 ? (
          <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">关联的 Provider 暂无可选账号。</div>
        ) : (
          <>
            <div className="grid grid-cols-3 gap-2">
              <div className="rounded-lg border bg-card p-3"><div className="text-xs text-muted-foreground">Provider</div><div className="mt-1 text-lg font-semibold">{providerGroups.length}</div></div>
              <div className="rounded-lg border bg-card p-3"><div className="text-xs text-muted-foreground">可选账号</div><div className="mt-1 text-lg font-semibold">{rows.length}</div></div>
              <div className="rounded-lg border bg-card p-3"><div className="text-xs text-muted-foreground">已选择</div><div className="mt-1 text-lg font-semibold">{selectedCount}</div></div>
            </div>
            {hasDirty ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2 text-xs">
                <span className="text-muted-foreground">
                  你的改动：<b className="text-success">新增 {addedCount}</b> · <b className="text-destructive">取消 {removedCount}</b>
                </span>
                <button
                  type="button"
                  onClick={() => setOverrides({})}
                  className="font-medium text-primary hover:underline"
                >
                  撤销全部改动
                </button>
              </div>
            ) : null}
            <div className="flex flex-col gap-3 rounded-lg border bg-muted/20 p-3 sm:flex-row sm:items-center">
              <input
                type="search"
                value={accountSearch}
                onChange={(event) => setAccountSearch(event.target.value)}
                placeholder="搜索账号或密钥标识"
                className={`${inputCls} w-full sm:max-w-xs`}
              />
              <div className="flex flex-wrap gap-4 sm:ml-auto">
                <Switch checked={availableOnly} onChange={setAvailableOnly} label="只看可用" />
                <Switch checked={selectedOnly} onChange={setSelectedOnly} label="只看已选" />
              </div>
            </div>
            <div className="space-y-5">
              {providerGroups.map((providerGroup) => {
                const providerRows = visibleRows.filter((row) => row.providerGroup.providerId === providerGroup.providerId);
                if (providerRows.length === 0) return null;
                return (
                  <section key={providerGroup.key} className="space-y-2">
                    <div className="flex items-center justify-between">
                      <h3 className="font-mono text-sm font-medium">{providerGroup.providerId}</h3>
                      <span className="text-xs text-muted-foreground">{providerRows.length} 个账号</span>
                    </div>
                    <div className="space-y-2">
                      {providerRows.map((row) => {
                        const keyHint = row.credential.keyPrefix || row.credential.keySuffix
                          ? `${row.credential.keyPrefix ?? '****'}…${row.credential.keySuffix ?? '****'}`
                          : '已托管密钥';
                        const disabledAccount = row.credential.status !== 'active';
                        const isAdded = row.dirty && row.selected && !row.defaultSelected;
                        const isRemoved = row.dirty && !row.selected && row.defaultSelected;
                        return (
                          <button
                            key={row.overrideKey}
                            type="button"
                            disabled={disabledAccount}
                            onClick={() => setOverrides((current) => ({ ...current, [row.overrideKey]: !row.selected }))}
                            className={`flex w-full cursor-pointer flex-col gap-3 rounded-lg border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 sm:flex-row sm:items-center ${row.selected ? 'border-primary bg-primary/5 ring-1 ring-primary/30' : 'hover:bg-muted/30'}`}
                          >
                            <div className="min-w-0 flex-1">
                              <div className="flex flex-wrap items-center gap-2">
                                <span className="font-medium">上游账号 {String(row.index + 1).padStart(2, '0')}</span>
                                <Badge variant={row.credential.status === 'active' ? 'success' : 'secondary'}>
                                  {row.credential.status === 'active' ? '可用' : '停用'}
                                </Badge>
                                {isAdded ? <Badge variant="default">新选</Badge> : null}
                                {isRemoved ? <Badge variant="destructive">已取消</Badge> : null}
                              </div>
                              <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{keyHint}</div>
                            </div>
                            <div className="grid grid-cols-3 gap-3 text-xs text-muted-foreground sm:text-right">
                              <span>RPM <b className="block text-foreground">{row.credential.rpm ?? '—'}</b></span>
                              <span>TPM <b className="block text-foreground">{row.credential.tpm ?? '—'}</b></span>
                              <span>优先级 <b className="block text-foreground">{row.credential.priority}</b></span>
                            </div>
                          </button>
                        );
                      })}
                    </div>
                  </section>
                );
              })}
              {visibleRows.length === 0 ? <div className="rounded-lg border border-dashed py-8 text-center text-sm text-muted-foreground">没有符合筛选条件的账号</div> : null}
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

// ---------------------------------------------------------------------------
// 路由配置抽屉：只读路由列表 + 编辑/新增走弹窗
// ---------------------------------------------------------------------------

function RouteConfigSheet({
  group,
  providers,
  credentials,
  onClose,
}: {
  group: RouteModelGroup;
  providers: AdminProvider[];
  credentials: AdminUpstreamCredential[];
  onClose: () => void;
}) {
  const activeProviders = useMemo(
    () => providers.filter((provider) => provider.status !== 'deleted').sort((a, b) => a.id.localeCompare(b.id)),
    [providers],
  );
  const sortedRoutes = useMemo(() => [...group.routes].sort((a, b) => {
    const provider = a.providerId.localeCompare(b.providerId);
    if (provider !== 0) return provider;
    const protocol = a.protocol.localeCompare(b.protocol);
    if (protocol !== 0) return protocol;
    return a.upstreamModel.localeCompare(b.upstreamModel);
  }), [group.routes]);

  const [editingRoute, setEditingRoute] = useState<AdminRouteConfig | null>(null);
  const [creating, setCreating] = useState(false);

  return (
    <Sheet
      open
      onClose={onClose}
      title="配置路由"
      description={group.modelId}
      width="lg"
      footer={
        <div className="flex justify-end">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>完成</Button>
        </div>
      }
    >
      <div className="space-y-4">
        <div className="rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">
          配置 <span className="font-medium text-foreground">{group.modelId}</span> 的模型转发能力：选择 Provider、能力协议，并指定真正发给上游的模型名。已有 {sortedRoutes.length} 条路由。
        </div>

        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium">已有路由</h3>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setCreating(true)}
          >
            新增路由
          </Button>
        </div>

        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full text-sm">
            <thead className="bg-muted/30 text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 text-left font-medium">启用</th>
                <th className="px-4 py-2 text-left font-medium">Provider</th>
                <th className="px-4 py-2 text-left font-medium">能力</th>
                <th className="px-4 py-2 text-left font-medium">上游模型</th>
                <th className="w-20 px-4 py-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sortedRoutes.map((route) => (
                <tr key={route.id} className="hover:bg-muted/20">
                  <td className="px-4 py-3 align-middle">
                    {route.enabled ? <Badge variant="success">启用</Badge> : <Badge variant="secondary">停用</Badge>}
                    {route.quarantined ? <Badge variant="destructive" className="ml-1">异常</Badge> : null}
                  </td>
                  <td className="px-4 py-3 align-middle font-mono text-xs font-medium">{route.providerId}</td>
                  <td className="px-4 py-3 align-middle"><Badge variant="secondary">{protocolLabel(route.protocol)}</Badge></td>
                  <td className="px-4 py-3 align-middle font-mono text-xs text-muted-foreground">{route.upstreamModel || '—'}</td>
                  <td className="px-4 py-3 text-right">
                    <Button type="button" variant="outline" size="sm" onClick={() => setEditingRoute(route)}>编辑</Button>
                  </td>
                </tr>
              ))}
              {sortedRoutes.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-sm text-muted-foreground">暂无路由，点击「新增路由」创建。</td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>

        <div className="text-xs text-muted-foreground">
          新增/编辑路由后需点编译并发布配置才会影响 Executor。
        </div>
      </div>

      {editingRoute ? (
        <EditRouteModal
          route={editingRoute}
          providers={activeProviders}
          onClose={() => setEditingRoute(null)}
        />
      ) : null}
      {creating ? (
        <NewRouteModal
          modelId={group.modelId}
          providers={activeProviders}
          credentials={credentials}
          onClose={() => setCreating(false)}
        />
      ) : null}
    </Sheet>
  );
}

function EditRouteModal({
  route,
  providers,
  onClose,
}: {
  route: AdminRouteConfig;
  providers: AdminProvider[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [upstreamModel, setUpstreamModel] = useState(route.upstreamModel);
  const [enabled, setEnabled] = useState(route.enabled);

  const saveMut = useMutation({
    mutationFn: () => adminConfigApi.updateRoute(route.id, {
      upstreamModel: upstreamModel.trim(),
      enabled,
    }),
    onSuccess: () => {
      toast.success('路由已更新（需点编译并发布生效）');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '保存失败'),
  });

  const provider = providers.find((p) => p.id === route.providerId);

  return (
    <Modal
      open
      onClose={onClose}
      title="编辑路由"
      description={`${route.modelId} · ${route.providerId} · ${protocolLabel(route.protocol)}`}
      maxWidth="md"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => saveMut.mutate()}
          submitLabel="保存"
          submitting={saveMut.isPending}
          disabled={saveMut.isPending}
        />
      }
    >
      <FormSection cols={1}>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs text-muted-foreground">模型</div>
            <div className="mt-1 break-all font-mono text-sm font-medium">{route.modelId}</div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs text-muted-foreground">Provider</div>
            <div className="mt-1 break-all font-mono text-sm font-medium">{provider?.displayLabel || route.providerId}</div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs text-muted-foreground">能力协议</div>
            <div className="mt-1 text-sm font-medium">{protocolLabel(route.protocol)}</div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs text-muted-foreground">优先级</div>
            <div className="mt-1 text-sm font-medium tabular-nums">{route.priority}</div>
          </div>
        </div>
        <Field label="上游模型" required>
          <TextField value={upstreamModel} onChange={setUpstreamModel} placeholder="真正发给上游的模型名" className="font-mono" />
        </Field>
        <div className="flex items-center justify-between rounded-lg border p-3">
          <div>
            <p className="text-sm font-medium">启用此路由</p>
            <p className="mt-0.5 text-xs text-muted-foreground">停用后该 Provider 协议不再承接此模型的请求</p>
          </div>
          <Switch checked={enabled} onChange={setEnabled} />
        </div>
      </FormSection>
    </Modal>
  );
}

function NewRouteModal({
  modelId,
  providers,
  credentials,
  onClose,
}: {
  modelId: string;
  providers: AdminProvider[];
  credentials: AdminUpstreamCredential[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [providerId, setProviderId] = useState(providers[0]?.id ?? '');
  const [protocols, setProtocols] = useState<Set<string>>(new Set(['openai_chat']));
  const [upstreamModel, setUpstreamModel] = useState(modelId);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const providerOptions = providers.map((p) => ({ value: p.id, label: p.displayLabel || p.id }));
  const providerCredentials = useMemo(
    () => credentials.filter((c) => c.providerId === providerId && c.status !== 'deleted'),
    [credentials, providerId],
  );
  const providerAccounts = useMemo(() => {
    const grouped = new Map<string, AdminUpstreamCredential[]>();
    for (const credential of providerCredentials) {
      const logicalId = logicalCredentialId(credential.id);
      grouped.set(logicalId, [...(grouped.get(logicalId) ?? []), credential]);
    }
    return Array.from(grouped.entries()).map(([logicalId, accountCredentials], index) => {
      const primary = (accountCredentials.find((c) => c.status === 'active') ?? accountCredentials[0])!;
      return {
        logicalId,
        credentialIds: accountCredentials.map((c) => c.id),
        credential: primary,
        index,
      };
    });
  }, [providerCredentials]);

  const toggleAccount = (logicalId: string, ids: string[]) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(logicalId)) {
        next.delete(logicalId);
        for (const id of ids) next.delete(id);
      } else {
        next.add(logicalId);
        for (const id of ids) next.add(id);
      }
      return next;
    });
  };

  const toggleProtocol = (value: string) => {
    setProtocols((current) => {
      const next = new Set(current);
      if (next.has(value)) next.delete(value);
      else next.add(value);
      return next;
    });
  };

  const createMut = useMutation({
    mutationFn: async () => {
      const selectedProtocols = PROTOCOL_OPTIONS.filter((p) => protocols.has(p));
      const bindingsFor = (routeId: string) => selectedIds.size > 0
        ? providerAccounts
            .filter((account) => account.credentialIds.some((id) => selectedIds.has(id)))
            .flatMap((account) =>
              account.credentialIds
                .filter((id) => selectedIds.has(id))
                .map((credentialId) => ({
                  routeId,
                  credentialId,
                  priority: account.credential.priority,
                  enabled: true,
                  rpm: null,
                  tpm: null,
                })),
            )
        : [];
      const results: { protocol: string; ok: boolean; error?: string }[] = [];
      for (const protocol of selectedProtocols) {
        try {
          const created = await adminConfigApi.createRoute({
            id: routeIdFor(modelId, providerId, protocol, upstreamModel),
            modelId,
            providerId,
            protocol,
            upstreamModel: upstreamModel.trim(),
            priority: 0,
          });
          const bindings = bindingsFor(created.id);
          if (bindings.length > 0) {
            await adminConfigApi.setRouteCredentials(created.id, bindings);
          }
          results.push({ protocol, ok: true });
        } catch (e) {
          results.push({ protocol, ok: false, error: e instanceof Error ? e.message : String(e) });
        }
      }
      const failed = results.filter((r) => !r.ok);
      if (failed.length > 0) {
        throw new Error(`${results.length - failed.length}/${results.length} 条创建成功，失败：${failed.map((f) => protocolLabel(f.protocol)).join('、')}`);
      }
    },
    onSuccess: () => {
      const count = PROTOCOL_OPTIONS.filter((p) => protocols.has(p)).length;
      toast.success(`已创建 ${count} 条路由（需点编译并发布生效）`);
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '创建失败'),
  });

  const valid = providerId.trim() && protocols.size > 0 && upstreamModel.trim();
  const selectedProtocolCount = PROTOCOL_OPTIONS.filter((p) => protocols.has(p)).length;

  return (
    <Modal
      open
      onClose={onClose}
      title="新增路由"
      description={modelId}
      maxWidth="md"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => createMut.mutate()}
          submitLabel={selectedProtocolCount > 1 ? `创建 ${selectedProtocolCount} 条路由` : '创建路由'}
          submitting={createMut.isPending}
          disabled={!valid || createMut.isPending}
        />
      }
    >
      <FormSection cols={1}>
        <div className="rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground">
          为模型 <span className="font-medium text-foreground">{modelId}</span> 新增转发路由：可多选能力协议，每个协议各创建一条路由；选择的账号会绑定到全部新建路由。
        </div>
        <Field label="Provider" required>
          <SelectField value={providerId} onChange={(v) => { setProviderId(v); setSelectedIds(new Set()); }} options={providerOptions} />
        </Field>
        <Field label="能力协议" required hint={`已选 ${selectedProtocolCount} 个，将为每个协议各创建一条路由`}>
          <div className="flex flex-wrap gap-2">
            {PROTOCOL_OPTIONS.map((p) => {
              const selected = protocols.has(p);
              return (
                <button
                  key={p}
                  type="button"
                  onClick={() => toggleProtocol(p)}
                  className={`rounded-md border px-3 py-1.5 text-sm transition-colors ${selected ? 'border-primary bg-primary/10 font-medium text-primary' : 'hover:bg-muted/30'}`}
                >
                  {protocolLabel(p)}
                </button>
              );
            })}
          </div>
        </Field>
        <Field label="上游模型" required>
          <TextField value={upstreamModel} onChange={setUpstreamModel} placeholder="真正发给上游的模型名" className="font-mono" />
        </Field>
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-foreground">上游账号</span>
            <span className="text-xs text-muted-foreground">已选 {providerAccounts.filter((a) => a.credentialIds.some((id) => selectedIds.has(id))).length}</span>
          </div>
          {providerAccounts.length === 0 ? (
            <div className="rounded-lg border border-dashed py-6 text-center text-sm text-muted-foreground">该 Provider 暂无上游账号</div>
          ) : (
            <div className="max-h-60 space-y-2 overflow-y-auto rounded-lg border bg-card p-2">
              {providerAccounts.map((account) => {
                const selected = account.credentialIds.some((id) => selectedIds.has(id));
                const keyHint = account.credential.keyPrefix || account.credential.keySuffix
                  ? `${account.credential.keyPrefix ?? '****'}…${account.credential.keySuffix ?? '****'}`
                  : '已托管密钥';
                const disabledAccount = account.credential.status !== 'active';
                return (
                  <button
                    key={account.logicalId}
                    type="button"
                    disabled={disabledAccount}
                    onClick={() => toggleAccount(account.logicalId, account.credentialIds)}
                    className={`flex w-full items-center gap-3 rounded-md border p-2 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${selected ? 'border-primary bg-primary/5' : 'hover:bg-muted/30'}`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">账号 {String(account.index + 1).padStart(2, '0')}</span>
                        <Badge variant={account.credential.status === 'active' ? 'success' : 'secondary'}>
                          {account.credential.status === 'active' ? '可用' : '停用'}
                        </Badge>
                      </div>
                      <div className="mt-0.5 break-all font-mono text-xs text-muted-foreground">{keyHint}</div>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </FormSection>
    </Modal>
  );
}

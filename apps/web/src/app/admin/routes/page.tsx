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
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
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

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '正常' },
  { key: 'disabled', label: '已禁用' },
];

function logicalCredentialId(id: string): string {
  return id.replace(/-(openai|anthropic|responses)$/u, '');
}

function routeIdFor(modelId: string, providerId: string, upstreamModel: string): string {
  const safe = (value: string) => value.trim().toLowerCase().replace(/[^a-z0-9._-]+/gu, '-').replace(/^-+|-+$/gu, '') || 'route';
  return `${safe(modelId)}-${safe(providerId)}-${safe(upstreamModel)}`.slice(0, 180);
}

function routeProviderGroup(route: AdminRouteConfig): RouteProviderGroup {
  return { key: route.id, modelId: route.modelId, providerId: route.providerId, routes: [route] };
}

function sortRoutes(routes: AdminRouteConfig[]): AdminRouteConfig[] {
  return [...routes].sort((a, b) => {
    const byModel = a.modelId.localeCompare(b.modelId);
    if (byModel !== 0) return byModel;
    const byProvider = a.providerId.localeCompare(b.providerId);
    if (byProvider !== 0) return byProvider;
    return a.upstreamModel.localeCompare(b.upstreamModel);
  });
}

export default function AdminRoutesPage() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [pickerRoute, setPickerRoute] = useState<AdminRouteConfig | null>(null);
  const [editingRoute, setEditingRoute] = useState<AdminRouteConfig | null>(null);
  const [creating, setCreating] = useState(false);

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
  const { data: models = [] } = useQuery({
    queryKey: ['admin', 'models'],
    queryFn: adminConfigApi.listModels,
  });

  const routeIdsKey = useMemo(() => routes.map((route) => route.id).sort().join('|'), [routes]);
  const { data: allRouteCredentialSets = [] } = useQuery({
    queryKey: ['admin', 'route-credentials', 'all-routes', routeIdsKey],
    queryFn: async () => Promise.all(routes.map((route) => adminConfigApi.listRouteCredentials(route.id))),
    enabled: routes.length > 0,
  });

  const routeModelOptions = useMemo(() => {
    // Models come from the models table (immediately persisted on create),
    // not from existing routes, so a newly created model is selectable in
    // the route dropdown without needing a compile/publish round-trip.
    const ids = new Set<string>();
    models.forEach((m) => ids.add(m.id));
    // also include any model referenced by an existing route but missing from
    // the models table (defensive), so nothing disappears from the filter.
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

  const sortedRoutes = useMemo(() => sortRoutes(filtered), [filtered]);

  const routeStats = useMemo(() => ({
    models: models.length,
    routes: routes.length,
    providers: new Set(routes.map((route) => route.providerId)).size,
    quarantined: routes.filter((route) => route.quarantined).length,
  }), [models, routes]);

  const routeCredentialsByRouteId = useMemo(() => {
    const map = new Map<string, AdminRouteCredential[]>();
    routes.forEach((route, index) => map.set(route.id, allRouteCredentialSets[index] ?? []));
    return map;
  }, [allRouteCredentialSets, routes]);

  const credentialStatus = useMemo(
    () => new Map(credentials.map((credential) => [credential.id, credential.status])),
    [credentials],
  );

  const routeAccountCount = useMemo(() => {
    const map = new Map<string, { active: number; total: number }>();
    for (const route of routes) {
      const bindings = routeCredentialsByRouteId.get(route.id) ?? [];
      const ids = new Set<string>();
      const activeIds = new Set<string>();
      for (const binding of bindings) {
        if (!binding.enabled) continue;
        const logicalId = logicalCredentialId(binding.credentialId);
        ids.add(logicalId);
        if (credentialStatus.get(binding.credentialId) === 'active') activeIds.add(logicalId);
      }
      map.set(route.id, { active: activeIds.size, total: ids.size });
    }
    return map;
  }, [routeCredentialsByRouteId, credentialStatus, routes]);

  const activeProviders = useMemo(
    () => providers.filter((provider) => provider.status !== 'deleted').sort((a, b) => a.id.localeCompare(b.id)),
    [providers],
  );

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
          placeholder="搜索路由 ID / Provider / 模型 / 上游模型"
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
      ) : sortedRoutes.length === 0 ? (
        <div className="rounded-xl border border-dashed py-16 text-center text-sm text-muted-foreground">暂无路由配置</div>
      ) : (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium">
              路由列表 <span className="text-muted-foreground">({sortedRoutes.length})</span>
            </h2>
            <Button type="button" variant="outline" size="sm" onClick={() => setCreating(true)}>新增路由</Button>
          </div>

          {/* Desktop table */}
          <div className="hidden overflow-x-auto rounded-lg border bg-card md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-24">状态</TableHead>
                  <TableHead>路由 ID</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead>上游模型</TableHead>
                  <TableHead className="text-center">账号</TableHead>
                  <TableHead className="w-28 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedRoutes.map((route) => {
                  const count = routeAccountCount.get(route.id) ?? { active: 0, total: 0 };
                  return (
                    <TableRow key={route.id}>
                      <TableCell>
                        {route.enabled ? <Badge variant="success">启用</Badge> : <Badge variant="secondary">停用</Badge>}
                        {route.quarantined ? <Badge variant="destructive" className="ml-1">异常</Badge> : null}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{route.id}</TableCell>
                      <TableCell className="font-mono text-xs">{route.modelId}</TableCell>
                      <TableCell className="font-mono text-xs">{route.providerId}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{route.upstreamModel || '—'}</TableCell>
                      <TableCell className="text-center">
                        <Badge variant={count.active > 0 ? 'default' : 'secondary'} className="tabular-nums">
                          {count.active}/{count.total}
                        </Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-right">
                        <Button type="button" variant="ghost" size="sm" onClick={() => setPickerRoute(route)}>账号</Button>
                        <Button type="button" variant="outline" size="sm" onClick={() => setEditingRoute(route)}>编辑</Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* Mobile card list */}
          <div className="space-y-2 md:hidden">
            {sortedRoutes.map((route) => {
              const count = routeAccountCount.get(route.id) ?? { active: 0, total: 0 };
              return (
                <div key={route.id} className="rounded-lg border bg-card p-3 space-y-1.5">
                  <button
                    type="button"
                    onClick={() => setEditingRoute(route)}
                    className="w-full text-left space-y-1.5 active:bg-accent/50 transition-colors"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-mono text-xs">{route.id}</span>
                      <div className="flex shrink-0 gap-1">
                        {route.enabled ? <Badge variant="success">启用</Badge> : <Badge variant="secondary">停用</Badge>}
                        {route.quarantined ? <Badge variant="destructive">异常</Badge> : null}
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
                      <span className="font-mono">{route.modelId}</span>
                      <span>·</span>
                      <span className="font-mono">{route.providerId}</span>
                      <span>·</span>
                      <span className="font-mono">{route.upstreamModel || '—'}</span>
                    </div>
                    <div className="text-xs text-muted-foreground">账号 {count.active}/{count.total}</div>
                  </button>
                  <div className="flex justify-end gap-2 pt-1">
                    <Button type="button" variant="ghost" size="sm" onClick={() => setPickerRoute(route)}>账号</Button>
                    <Button type="button" variant="outline" size="sm" onClick={() => setEditingRoute(route)}>编辑</Button>
                  </div>
                </div>
              );
            })}
          </div>

          <div className="text-xs text-muted-foreground">
            新增/编辑路由后已保存到 DB；点「编译并发布」后才会对 Executor 生效。
          </div>
        </div>
      )}

      {pickerRoute ? (
        <AccountPickerModal
          title="配置账号"
          description={`${pickerRoute.modelId} · ${pickerRoute.providerId}`}
          hint={`为 ${pickerRoute.modelId} 的 ${pickerRoute.providerId} 路由选择可参与的上游账号。`}
          providerGroups={[routeProviderGroup(pickerRoute)]}
          credentials={credentials}
          onClose={() => setPickerRoute(null)}
        />
      ) : null}
      {editingRoute ? (
        <EditRouteModal
          route={editingRoute}
          providers={activeProviders}
          onClose={() => setEditingRoute(null)}
        />
      ) : null}
      {creating ? (
        <NewRouteModal
          modelOptions={routeModelOptions}
          providers={activeProviders}
          credentials={credentials}
          onClose={() => setCreating(false)}
        />
      ) : null}
    </div>
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
      toast.success('账号选择已保存。编译发布后对 Executor 生效。');
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
// 编辑路由弹窗
// ---------------------------------------------------------------------------

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
      toast.success('路由已更新。编译发布后对 Executor 生效。');
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
      description={`${route.modelId} · ${route.providerId}`}
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
            <div className="text-xs text-muted-foreground">优先级</div>
            <div className="mt-1 text-sm font-medium tabular-nums">{route.priority}</div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs text-muted-foreground">路由 ID</div>
            <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{route.id}</div>
          </div>
        </div>
        <Field label="上游模型" required>
          <TextField value={upstreamModel} onChange={setUpstreamModel} placeholder="真正发给上游的模型名" className="font-mono" />
        </Field>
        <div className="flex items-center justify-between rounded-lg border p-3">
          <div>
            <p className="text-sm font-medium">启用此路由</p>
            <p className="mt-0.5 text-xs text-muted-foreground">停用后该 Provider 不再承接此模型的请求</p>
          </div>
          <Switch checked={enabled} onChange={setEnabled} />
        </div>
      </FormSection>
    </Modal>
  );
}

// ---------------------------------------------------------------------------
// 新增路由弹窗
// ---------------------------------------------------------------------------

function NewRouteModal({
  modelOptions,
  providers,
  credentials,
  onClose,
}: {
  modelOptions: string[];
  providers: AdminProvider[];
  credentials: AdminUpstreamCredential[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [modelId, setModelId] = useState('');
  const [providerId, setProviderId] = useState(providers[0]?.id ?? '');
  const [upstreamModel, setUpstreamModel] = useState('');
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

  const createMut = useMutation({
    mutationFn: async () => {
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
      const created = await adminConfigApi.createRoute({
        id: routeIdFor(modelId, providerId, upstreamModel),
        modelId,
        providerId,
        upstreamModel: upstreamModel.trim(),
        priority: 0,
      });
      const bindings = bindingsFor(created.id);
      if (bindings.length > 0) {
        await adminConfigApi.setRouteCredentials(created.id, bindings);
      }
    },
    onSuccess: () => {
      toast.success('已创建路由。编译发布后对 Executor 生效。');
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-configs'] });
      queryClient.invalidateQueries({ queryKey: ['admin', 'route-credentials'] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : '创建失败'),
  });

  const valid = modelId.trim() && providerId.trim() && upstreamModel.trim();

  return (
    <Modal
      open
      onClose={onClose}
      title="新增路由"
      maxWidth="md"
      footer={
        <FormActions
          onCancel={onClose}
          onSubmit={() => createMut.mutate()}
          submitLabel="创建路由"
          submitting={createMut.isPending}
          disabled={!valid || createMut.isPending}
        />
      }
    >
      <FormSection cols={1}>
        <div className="rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground">
          为模型新增转发路由：选择 Provider 与上游模型名；支持的协议由 Provider 的端点决定。选择的账号会绑定到新建路由。
        </div>
        <Field label="模型" required>
          <SelectField
            value={modelId}
            onChange={setModelId}
            options={modelOptions.map((id) => ({ value: id, label: id }))}
            placeholder="选择已有模型"
          />
        </Field>
        <Field label="Provider" required>
          <SelectField value={providerId} onChange={(v) => { setProviderId(v); setSelectedIds(new Set()); }} options={providerOptions} />
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

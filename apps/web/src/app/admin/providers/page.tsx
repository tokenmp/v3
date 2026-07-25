'use client';

import { useQuery } from '@tanstack/react-query';
import { adminConfigApi } from '@/lib/api/admin';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AlertCircle } from 'lucide-react';
import type { AdminProvider } from '@/types/admin';

const SDK_LABELS: Record<AdminProvider['sdkKind'], string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
};

const STATUS_MAP: Record<
  AdminProvider['status'],
  { label: string; variant: 'success' | 'secondary' | 'destructive' }
> = {
  active: { label: '启用', variant: 'success' },
  disabled: { label: '禁用', variant: 'secondary' },
  deleted: { label: '已删除', variant: 'destructive' },
};

export default function AdminProvidersPage() {
  const { data: providers = [], isLoading } = useQuery({
    queryKey: ['admin', 'providers'],
    queryFn: adminConfigApi.listProviders,
  });

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      {/* Info banner */}
      <div className="flex items-start gap-3 rounded-lg border bg-muted/40 p-4">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
        <p className="text-sm text-muted-foreground">
          Provider 与路由配置来自 Executor Config Snapshot，当前为只读展示。编辑需通过 Config Service（待实现）。
        </p>
      </div>

      {providers.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无 Provider</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>Selector</TableHead>
                  <TableHead>SDK</TableHead>
                  <TableHead>协议</TableHead>
                  <TableHead>BaseURL</TableHead>
                  <TableHead className="text-center">凭据数</TableHead>
                  <TableHead className="text-center">路由数</TableHead>
                  <TableHead>状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {providers.map((p) => {
                  const st = STATUS_MAP[p.status];
                  return (
                    <TableRow key={p.id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{p.name}</span>
                          <Badge variant="outline" className="font-mono">
                            {p.displayLabel}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{p.selector}</TableCell>
                      <TableCell>
                        <Badge variant="default">{SDK_LABELS[p.sdkKind] ?? p.sdkKind}</Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{p.protocol}</TableCell>
                      <TableCell className="max-w-[220px] truncate" title={p.baseURL}>
                        {p.baseURL}
                      </TableCell>
                      <TableCell className="text-center">
                        {p.credentialCount === 0 ? (
                          <span className="text-destructive">无凭据</span>
                        ) : (
                          p.credentialCount
                        )}
                      </TableCell>
                      <TableCell className="text-center">{p.routeCount}</TableCell>
                      <TableCell>
                        <Badge variant={st.variant}>{st.label}</Badge>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {providers.map((p) => {
              const st = STATUS_MAP[p.status];
              return (
                <Card key={p.id}>
                  <CardContent className="p-4 space-y-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-medium">{p.name}</span>
                        <Badge variant="outline" className="font-mono">
                          {p.displayLabel}
                        </Badge>
                      </div>
                      <Badge variant={st.variant} className="shrink-0">
                        {st.label}
                      </Badge>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Badge variant="default">{SDK_LABELS[p.sdkKind] ?? p.sdkKind}</Badge>
                      <span className="text-sm text-muted-foreground">{p.protocol}</span>
                    </div>
                    <div className="text-sm text-muted-foreground space-y-1">
                      <p>
                        凭据：{p.credentialCount === 0 ? (
                          <span className="text-destructive">无凭据</span>
                        ) : (
                          p.credentialCount
                        )}{' '}
                        · 路由：{p.routeCount}
                      </p>
                      <p className="truncate" title={p.baseURL}>{p.baseURL}</p>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

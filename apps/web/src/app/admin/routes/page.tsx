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
import type { AdminRouteConfig } from '@/types/admin';

function StatusBadge({ enabled, quarantined }: { enabled: boolean; quarantined: boolean }) {
  if (!enabled) return <Badge variant="secondary">已禁用</Badge>;
  if (quarantined) return <Badge variant="warning">已隔离</Badge>;
  return <Badge variant="success">正常</Badge>;
}

export default function AdminRoutesPage() {
  const { data: routes = [], isLoading } = useQuery({
    queryKey: ['admin', 'route-configs'],
    queryFn: adminConfigApi.listRoutes,
  });

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      {/* Info bar */}
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
        <span>路由配置来自 Executor Config Snapshot，只读展示。编辑需通过 Config Service（待实现）。</span>
      </div>

      {routes.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无路由配置</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
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
                </TableRow>
              </TableHeader>
              <TableBody>
                {routes.map((r: AdminRouteConfig) => (
                  <TableRow key={r.id}>
                    <TableCell className="font-mono text-xs">{r.id}</TableCell>
                    <TableCell className="font-mono text-xs">{r.modelId}</TableCell>
                    <TableCell className="font-mono text-xs">{r.providerId}</TableCell>
                    <TableCell className="font-mono">{r.upstreamModel}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className="font-mono">
                        {r.protocol}
                      </Badge>
                    </TableCell>
                    <TableCell>{r.priority}</TableCell>
                    <TableCell>
                      <StatusBadge enabled={r.enabled} quarantined={r.quarantined} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {routes.map((r: AdminRouteConfig) => (
              <Card key={r.id}>
                <CardContent className="p-4 space-y-2">
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-mono text-xs text-muted-foreground">{r.id}</span>
                    <StatusBadge enabled={r.enabled} quarantined={r.quarantined} />
                  </div>
                  <div className="text-sm space-y-1">
                    <p>
                      <span className="font-mono text-xs">{r.modelId}</span>
                      <span className="mx-1 text-muted-foreground">→</span>
                      <span className="font-mono text-xs">{r.providerId}</span>
                    </p>
                    <p className="font-mono">{r.upstreamModel}</p>
                    <p>
                      <Badge variant="outline" className="font-mono">
                        {r.protocol}
                      </Badge>
                    </p>
                    <p className="text-muted-foreground">优先级：{r.priority}</p>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

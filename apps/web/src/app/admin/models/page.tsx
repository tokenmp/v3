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
import type { AdminModelConfig } from '@/types/admin';

const CAPABILITY_LABELS: Record<string, string> = {
  text: '文本',
  tools: '工具',
  vision: '视觉',
  thinking: '思考',
  image: '图像',
};

function CapabilityBadges({ capabilities }: { capabilities: string[] }) {
  return (
    <div className="flex gap-1 flex-wrap">
      {capabilities.map((cap) => (
        <Badge key={cap} variant="secondary">
          {CAPABILITY_LABELS[cap] ?? cap}
        </Badge>
      ))}
    </div>
  );
}

function ThinkingBadge({ supported }: { supported: boolean }) {
  return supported ? (
    <Badge variant="success">支持</Badge>
  ) : (
    <Badge variant="secondary">不支持</Badge>
  );
}

export default function AdminModelsPage() {
  const { data: models = [], isLoading } = useQuery({
    queryKey: ['admin', 'model-configs'],
    queryFn: adminConfigApi.listModels,
  });

  if (isLoading) {
    return <div className="p-6 text-muted-foreground">加载中…</div>;
  }

  return (
    <div className="space-y-6">
      {/* Info bar */}
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
        <span>
          模型配置来自 Executor Config Snapshot，只读展示。编辑需通过 Config Service（待实现）。
        </span>
      </div>

      {models.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">暂无模型配置</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>显示名</TableHead>
                  <TableHead>能力</TableHead>
                  <TableHead>Thinking</TableHead>
                  <TableHead>路由数</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {models.map((m: AdminModelConfig) => (
                  <TableRow key={m.id}>
                    <TableCell className="font-mono text-xs">{m.id}</TableCell>
                    <TableCell className="font-medium">{m.displayName}</TableCell>
                    <TableCell>
                      <CapabilityBadges capabilities={m.capabilities} />
                    </TableCell>
                    <TableCell>
                      <ThinkingBadge supported={m.thinkingSupported} />
                    </TableCell>
                    <TableCell>{m.routeCount}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          {/* Mobile cards */}
          <div className="md:hidden space-y-3">
            {models.map((m: AdminModelConfig) => (
              <Card key={m.id}>
                <CardContent className="p-4 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <div className="font-medium">{m.displayName}</div>
                      <div className="font-mono text-xs text-muted-foreground">{m.id}</div>
                    </div>
                    <ThinkingBadge supported={m.thinkingSupported} />
                  </div>
                  <CapabilityBadges capabilities={m.capabilities} />
                  <div className="text-xs text-muted-foreground">
                    路由数：{m.routeCount}
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

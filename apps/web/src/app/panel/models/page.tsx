'use client';

import { useQuery } from '@tanstack/react-query';
import { request } from '@/lib/api/core';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';

/** OpenAI-style /v1/models entry (executor catalog). */
interface CatalogModel {
  id: string;
  object: 'model';
  owned_by?: string;
  created?: number;
  capabilities?: string[];
  thinking?: {
    supported: boolean;
    default_effort?: string;
    max_effort?: string;
    effort_levels?: string[];
    max_budget_tokens?: number;
  };
}

interface CatalogResponse {
  object: 'list';
  data: CatalogModel[];
}

async function listModels(): Promise<CatalogModel[]> {
  // Same-origin /v1/models; the JWT is attached by request() and Edge injects
  // the executor service token on the proxy hop.
  const res = await request<CatalogResponse>('/v1/models');
  return res.data ?? [];
}

const CAP_LABEL: Record<string, string> = {
  text: '文本',
  tools: '工具',
  vision: '视觉',
  thinking: '思考',
  image: '图像',
};

export default function PanelModelsPage() {
  const { data: models = [], isLoading } = useQuery({
    queryKey: ['catalog', 'models'] as const,
    queryFn: listModels,
  });

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold">模型列表</h1>
        <p className="text-sm text-muted-foreground">
          当前可用的模型目录。调用 <code className="rounded bg-muted px-1">model=auto</code> 时由平台按配置自动选择。
        </p>
      </div>

      {isLoading ? (
        <div className="rounded-lg border p-8 text-center text-sm text-muted-foreground">加载中…</div>
      ) : models.length === 0 ? (
        <div className="rounded-lg border p-8 text-center text-sm text-muted-foreground">暂无可用模型</div>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
          <div className="overflow-hidden rounded-lg border border-border bg-card">
            <Table>
              <TableHeader className="bg-muted/30">
                <TableRow>
                  <TableHead>模型 ID</TableHead>
                  <TableHead>能力</TableHead>
                  <TableHead>思考</TableHead>
                  <TableHead>提供方</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {models.map((m) => (
                  <TableRow key={m.id}>
                    <TableCell className="font-mono text-xs">{m.id}</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {(m.capabilities ?? []).map((cap) => (
                          <Badge key={cap} variant="secondary" className="text-xs">
                            {CAP_LABEL[cap] ?? cap}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {m.thinking?.supported
                        ? `支持（${m.thinking.default_effort ?? 'medium'} / ${m.thinking.max_effort ?? 'high'}）`
                        : '不支持'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{m.owned_by ?? '—'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {models.map((m) => (
              <div key={m.id} className="rounded-lg border bg-card p-3 space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs font-medium truncate">{m.id}</span>
                  <span className="text-xs text-muted-foreground shrink-0">{m.owned_by ?? '—'}</span>
                </div>
                <div className="flex flex-wrap gap-1">
                  {(m.capabilities ?? []).map((cap) => (
                    <Badge key={cap} variant="secondary" className="text-[10px]">
                      {CAP_LABEL[cap] ?? cap}
                    </Badge>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">
                  {m.thinking?.supported
                    ? `思考：${m.thinking.default_effort ?? 'medium'} / ${m.thinking.max_effort ?? 'high'}`
                    : '不支持思考'}
                </p>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

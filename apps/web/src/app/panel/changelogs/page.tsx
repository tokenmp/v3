'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { noticeApi } from '@/lib/api/notice';
import { Card, CardContent } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { GitCommitHorizontal, ChevronDown, ChevronUp } from 'lucide-react';
import { Markdown } from '@/components/markdown';
import type { Changelog } from '@/types';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function ChangelogRow({ item }: { item: Changelog }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <>
      <TableRow
        className="cursor-pointer focus-inset"
        onClick={() => setExpanded((v) => !v)}
        tabIndex={0}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setExpanded((v) => !v); } }}
      >
        <TableCell>
          <span className="text-lg font-bold font-mono">{item.version}</span>
        </TableCell>
        <TableCell className="text-sm">{item.title}</TableCell>
        <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
          {formatTime(item.published_at)}
        </TableCell>
        <TableCell className="w-8">
          {expanded ? <ChevronUp className="h-4 w-4 text-muted-foreground" /> : <ChevronDown className="h-4 w-4 text-muted-foreground" />}
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={4} className="bg-muted/30">
            <div className="py-3"><Markdown>{item.body}</Markdown></div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function ChangelogCard({ item }: { item: Changelog }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <Card
      className="cursor-pointer focus-inset"
      onClick={() => setExpanded((v) => !v)}
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setExpanded((v) => !v); } }}
    >
      <CardContent className="p-4 space-y-2">
        <div className="flex items-center justify-between">
          <span className="text-lg font-bold font-mono">{item.version}</span>
          {expanded ? <ChevronUp className="h-4 w-4 text-muted-foreground shrink-0" /> : <ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" />}
        </div>
        <p className="text-sm">{item.title}</p>
        <p className="text-xs text-muted-foreground">{formatTime(item.published_at)}</p>
        {expanded && (
          <div className="pt-2 border-t"><Markdown>{item.body}</Markdown></div>
        )}
      </CardContent>
    </Card>
  );
}

export default function ChangelogsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['changelogs'],
    queryFn: () => noticeApi.listChangelogs(),
  });

  const items = data?.items ?? [];

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">版本日志</h2>

      {isLoading && (
        <Card>
          <CardContent className="flex items-center justify-center py-16 text-muted-foreground">
            加载中…
          </CardContent>
        </Card>
      )}

      {!isLoading && items.length === 0 && (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-16 text-center">
            <GitCommitHorizontal className="h-12 w-12 text-muted-foreground mb-4" />
            <p className="text-muted-foreground">暂无版本日志</p>
          </CardContent>
        </Card>
      )}

      {!isLoading && items.length > 0 && (
        <>
          {/* Desktop table */}
          <div className="hidden md:block">
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>版本</TableHead>
                    <TableHead>标题</TableHead>
                    <TableHead>发布时间</TableHead>
                    <TableHead className="w-8" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((c) => (
                    <ChangelogRow key={c.id} item={c} />
                  ))}
                </TableBody>
              </Table>
            </Card>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {items.map((c) => (
              <ChangelogCard key={c.id} item={c} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

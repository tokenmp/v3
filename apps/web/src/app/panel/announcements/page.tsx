'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { noticeApi } from '@/lib/api/notice';
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
import { Megaphone, ChevronDown, ChevronUp } from 'lucide-react';
import { Markdown } from '@/components/markdown';
import type { Announcement, AnnouncementSeverity } from '@/types';

function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN');
}

function severityConfig(severity: AnnouncementSeverity) {
  switch (severity) {
    case 'warning':
      return { label: '警告', variant: 'warning' as const };
    case 'maintenance':
      return { label: '维护', variant: 'info' as const };
    default:
      return { label: '通知', variant: 'default' as const };
  }
}

function AnnouncementRow({ item }: { item: Announcement }) {
  const [expanded, setExpanded] = useState(false);
  const cfg = severityConfig(item.severity);

  return (
    <>
      <TableRow
        className="cursor-pointer focus-inset"
        onClick={() => setExpanded((v) => !v)}
        tabIndex={0}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setExpanded((v) => !v); } }}
      >
        <TableCell>
          <div className="flex items-center gap-2">
            <Badge variant={cfg.variant}>{cfg.label}</Badge>
            <span className="font-medium">{item.title}</span>
          </div>
        </TableCell>
        <TableCell className="text-sm text-muted-foreground max-w-xs truncate hidden md:table-cell">
          {item.summary}
        </TableCell>
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
            <div className="py-3 space-y-2">
              <p className="text-sm text-muted-foreground md:hidden">{item.summary}</p>
              <Markdown>{item.body}</Markdown>
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function AnnouncementCard({ item }: { item: Announcement }) {
  const [expanded, setExpanded] = useState(false);
  const cfg = severityConfig(item.severity);

  return (
    <Card
      className="cursor-pointer focus-inset"
      onClick={() => setExpanded((v) => !v)}
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setExpanded((v) => !v); } }}
    >
      <CardContent className="p-4 space-y-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <Badge variant={cfg.variant}>{cfg.label}</Badge>
            <span className="font-medium text-sm truncate">{item.title}</span>
          </div>
          {expanded ? <ChevronUp className="h-4 w-4 text-muted-foreground shrink-0" /> : <ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" />}
        </div>
        <p className="text-xs text-muted-foreground">{item.summary}</p>
        <p className="text-xs text-muted-foreground">{formatTime(item.published_at)}</p>
        {expanded && (
          <div className="pt-2 border-t"><Markdown>{item.body}</Markdown></div>
        )}
      </CardContent>
    </Card>
  );
}

export default function AnnouncementsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['announcements'],
    queryFn: () => noticeApi.listAnnouncements(),
  });

  const items = data?.items ?? [];

  return (
    <div className="space-y-6">

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
            <Megaphone className="h-12 w-12 text-muted-foreground mb-4" />
            <p className="text-muted-foreground">暂无公告</p>
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
                    <TableHead>标题</TableHead>
                    <TableHead>摘要</TableHead>
                    <TableHead>发布时间</TableHead>
                    <TableHead className="w-8" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((a) => (
                    <AnnouncementRow key={a.id} item={a} />
                  ))}
                </TableBody>
              </Table>
            </Card>
          </div>

          {/* Mobile card list */}
          <div className="md:hidden space-y-3">
            {items.map((a) => (
              <AnnouncementCard key={a.id} item={a} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

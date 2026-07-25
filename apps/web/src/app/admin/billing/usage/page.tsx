'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import type { AdminDashboardStats, ModelUsageRow, TopUserRow } from '@/types/admin';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Activity, Users, Zap } from 'lucide-react';
import {
  BarChart,
  Bar,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from 'recharts';

/* -------------------------------------------------------------------------- */
/* Helpers                                                                     */
/* -------------------------------------------------------------------------- */

function compactNumber(n: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(n);
}

function mmdd(iso: string): string {
  return iso.slice(5); // "MM-DD"
}

function successRate(row: ModelUsageRow): string {
  if (row.requests === 0) return '—';
  return `${((row.success / row.requests) * 100).toFixed(1)}%`;
}

/* -------------------------------------------------------------------------- */
/* Stat card                                                                   */
/* -------------------------------------------------------------------------- */

function StatCard({
  icon: Icon,
  label,
  value,
}: {
  icon: React.ElementType;
  label: string;
  value: string;
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 pb-2">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Icon className="h-4 w-4" />
        </div>
        <CardTitle className="text-base">{label}</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="text-2xl font-semibold tabular-nums">{value}</p>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Model usage table + mobile cards                                            */
/* -------------------------------------------------------------------------- */

function ModelUsageSection({ rows }: { rows: ModelUsageRow[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">今日模型用量</CardTitle>
      </CardHeader>
      <CardContent>
        {/* Desktop table */}
        <div className="hidden md:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>模型</TableHead>
                <TableHead className="text-right">请求数</TableHead>
                <TableHead className="text-right">成功率</TableHead>
                <TableHead className="text-right">Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.model}>
                  <TableCell className="font-medium">{r.model}</TableCell>
                  <TableCell className="text-right tabular-nums">{r.requests.toLocaleString()}</TableCell>
                  <TableCell className="text-right tabular-nums">{successRate(r)}</TableCell>
                  <TableCell className="text-right tabular-nums">{r.tokens.toLocaleString()}</TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-8 text-center text-muted-foreground">
                    暂无数据
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        {/* Mobile card list */}
        <div className="space-y-3 md:hidden">
          {rows.map((r) => (
            <div key={r.model} className="flex items-center justify-between rounded-lg border p-3">
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium truncate">{r.model}</p>
                <p className="text-xs text-muted-foreground">
                  {r.requests.toLocaleString()} 次请求 · {successRate(r)}
                </p>
              </div>
              <p className="text-sm tabular-nums">{r.tokens.toLocaleString()} tokens</p>
            </div>
          ))}
          {rows.length === 0 && (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无数据</p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Top users table + mobile cards                                              */
/* -------------------------------------------------------------------------- */

function TopUsersSection({ rows }: { rows: TopUserRow[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">今日 Top 用户</CardTitle>
      </CardHeader>
      <CardContent>
        {/* Desktop table */}
        <div className="hidden md:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>邮箱</TableHead>
                <TableHead className="text-right">请求数</TableHead>
                <TableHead className="text-right">Token</TableHead>
                <TableHead className="text-right">费用</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.email}>
                  <TableCell className="font-medium truncate max-w-[200px]">{r.email}</TableCell>
                  <TableCell className="text-right tabular-nums">{r.requests.toLocaleString()}</TableCell>
                  <TableCell className="text-right tabular-nums">{r.tokens.toLocaleString()}</TableCell>
                  <TableCell className="text-right tabular-nums">¥{Number(r.cost).toFixed(4)}</TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-8 text-center text-muted-foreground">
                    暂无数据
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        {/* Mobile card list */}
        <div className="space-y-3 md:hidden">
          {rows.map((r) => (
            <div key={r.email} className="flex items-center justify-between rounded-lg border p-3">
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium truncate">{r.email}</p>
                <p className="text-xs text-muted-foreground">
                  {r.requests.toLocaleString()} 次请求 · {r.tokens.toLocaleString()} tokens
                </p>
              </div>
              <p className="text-sm tabular-nums">¥{Number(r.cost).toFixed(4)}</p>
            </div>
          ))}
          {rows.length === 0 && (
            <p className="py-8 text-center text-sm text-muted-foreground">暂无数据</p>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* Page                                                                        */
/* -------------------------------------------------------------------------- */

const DAY_OPTIONS = [7, 15, 30] as const;

export default function AdminBillingUsagePage() {
  const [days, setDays] = useState<number>(7);

  const { data: stats } = useQuery<AdminDashboardStats>({
    queryKey: ['admin', 'dashboard', days],
    queryFn: () => adminApi.getDashboardStats(days),
  });

  const dash = stats ?? null;

  return (
    <div className="space-y-6">
      {/* ── Header row ── */}
      <div className="flex items-center justify-between">
        <span className="text-lg font-semibold">用量统计</span>
        <div className="flex gap-1">
          {DAY_OPTIONS.map((d) => (
            <Button
              key={d}
              variant={days === d ? 'default' : 'outline'}
              size="sm"
              onClick={() => setDays(d)}
            >
              {d}天
            </Button>
          ))}
        </div>
      </div>

      {/* ── Overview cards ── */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          icon={Activity}
          label="总请求数"
          value={dash ? dash.totalRequests.toLocaleString() : '—'}
        />
        <StatCard
          icon={Users}
          label="总用户数"
          value={dash ? dash.totalUsers.toLocaleString() : '—'}
        />
        <StatCard
          icon={Zap}
          label="今日 Token"
          value={dash ? dash.todayTokens.toLocaleString() : '—'}
        />
        <StatCard
          icon={Users}
          label="今日活跃用户"
          value={dash ? dash.todayActiveUsers.toLocaleString() : '—'}
        />
      </div>

      {/* ── Trend charts ── */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">趋势</CardTitle>
        </CardHeader>
        <CardContent className="space-y-6">
          {dash ? (
            <>
              {/* Token stacked bar chart */}
              <div>
                <p className="mb-2 text-sm text-muted-foreground">Token 用量</p>
                <ResponsiveContainer width="100%" height={250}>
                  <BarChart data={dash.trend} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis
                      dataKey="date"
                      tickFormatter={(v: string) => mmdd(v)}
                      tick={{ fontSize: 12 }}
                      tickLine={false}
                      axisLine={false}
                    />
                    <YAxis
                      tickFormatter={(v: number) => compactNumber(v)}
                      tick={{ fontSize: 12 }}
                      tickLine={false}
                      axisLine={false}
                      width={48}
                    />
                    <Tooltip
                      formatter={(value) => (typeof value === 'number' ? value.toLocaleString() : String(value))}
                      labelFormatter={(label) => String(label)}
                    />
                    <Legend />
                    <Bar dataKey="inputTokens" name="输入 Token" stackId="tokens" fill="#6366f1" />
                    <Bar dataKey="outputTokens" name="输出 Token" stackId="tokens" fill="#10b981" />
                  </BarChart>
                </ResponsiveContainer>
              </div>

              {/* Requests area chart */}
              <div>
                <p className="mb-2 text-sm text-muted-foreground">请求数</p>
                <ResponsiveContainer width="100%" height={250}>
                  <AreaChart data={dash.trend} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis
                      dataKey="date"
                      tickFormatter={(v: string) => mmdd(v)}
                      tick={{ fontSize: 12 }}
                      tickLine={false}
                      axisLine={false}
                    />
                    <YAxis
                      tickFormatter={(v: number) => compactNumber(v)}
                      tick={{ fontSize: 12 }}
                      tickLine={false}
                      axisLine={false}
                      width={48}
                    />
                    <Tooltip
                      formatter={(value) => (typeof value === 'number' ? value.toLocaleString() : String(value))}
                      labelFormatter={(label) => String(label)}
                    />
                    <Area
                      type="monotone"
                      dataKey="requests"
                      stroke="#f59e0b"
                      fill="#f59e0b"
                      fillOpacity={0.15}
                      strokeWidth={2}
                      name="请求数"
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </>
          ) : (
            <div className="flex h-[500px] items-center justify-center text-muted-foreground">
              —
            </div>
          )}
        </CardContent>
      </Card>

      {/* ── Model usage + Top users ── */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <ModelUsageSection rows={dash?.todayModelUsage ?? []} />
        <TopUsersSection rows={dash?.todayTopUsers ?? []} />
      </div>
    </div>
  );
}

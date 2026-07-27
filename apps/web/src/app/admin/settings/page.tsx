'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-4">
      <span className="text-sm text-muted-foreground sm:w-40 shrink-0">{label}</span>
      <span className="text-sm text-foreground">{value}</span>
    </div>
  );
}

const SERVICES = [
  { name: 'Edge/BFF (gateway)', url: '/healthz', port: '3002' },
  { name: 'Auth', url: '/healthz', port: '8080', via: 'Edge' },
  { name: 'Notice', url: '/healthz', port: '8086', via: 'Edge' },
  { name: 'Logging', url: '/healthz', port: '8083', via: 'Edge' },
  { name: 'Billing', url: '/healthz', port: '8085', via: 'Edge' },
  { name: 'Config', url: '/healthz', port: '8084', via: 'Edge' },
];

function ServiceStatusRow({ name, url, port, via }: { name: string; url: string; port?: string; via?: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['healthz', name],
    queryFn: async () => {
      try {
        const res = await fetch(url);
        return { ok: res.ok, status: res.status };
      } catch {
        return { ok: false, status: 0 };
      }
    },
    refetchInterval: 30000,
  });

  const isUp = data?.ok === true;

  return (
    <TableRow>
      <TableCell className="font-medium text-sm">{name}</TableCell>
      <TableCell className="text-xs text-muted-foreground tabular-nums">:{port ?? '—'}{via ? ` (${via})` : ''}</TableCell>
      <TableCell>
        <span
          className={cn(
            'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium',
            isLoading
              ? 'bg-muted text-muted-foreground'
              : isUp
                ? 'bg-green-100 text-green-700'
                : 'bg-red-100 text-red-700',
          )}
        >
          <span
            className={cn(
              'size-1.5 rounded-full',
              isLoading ? 'bg-muted-foreground' : isUp ? 'bg-green-500' : 'bg-red-500',
            )}
          />
          {isLoading ? '检查中…' : isUp ? '运行中' : '不可用'}
        </span>
      </TableCell>
    </TableRow>
  );
}

export default function AdminSettingsPage() {
  const [registrationEnabled, setRegistrationEnabled] = useState(true);
  const [maintenanceMode, setMaintenanceMode] = useState(false);

  return (
    <div className="grid gap-6">
      {/* 平台信息 */}
      <Card>
        <CardHeader>
          <CardTitle>平台信息</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <InfoRow label="平台名称" value="TokenMP" />
          <InfoRow label="版本" value="v3" />
          <InfoRow label="环境" value="Development" />
        </CardContent>
      </Card>

      {/* 认证配置 */}
      <Card>
        <CardHeader>
          <CardTitle>认证配置</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <InfoRow label="JWT 算法" value="Ed25519 (EdDSA)" />
          <InfoRow label="JWT 签发者" value="tokenmp-auth" />
          <InfoRow label="JWT 受众" value="tokenmp-web" />
          <InfoRow label="Access Token TTL" value="15分钟" />
        </CardContent>
      </Card>

      {/* 服务状态 */}
      <Card>
        <CardHeader>
          <CardTitle>服务状态</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="text-xs">服务</TableHead>
                <TableHead className="text-xs">端口</TableHead>
                <TableHead className="text-xs">状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {SERVICES.map((s) => (
                <ServiceStatusRow key={s.name} name={s.name} url={s.url} port={s.port} via={s.via} />
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* 功能开关 */}
      <Card>
        <CardHeader>
          <CardTitle>功能开关</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <label className="flex items-center gap-3">
            <Checkbox
              checked={registrationEnabled}
              onCheckedChange={(v) => setRegistrationEnabled(v as boolean)}
            />
            <div className="flex flex-col">
              <span className="text-sm text-foreground">用户注册</span>
              <span className="text-xs text-muted-foreground">允许新用户自行注册账号</span>
            </div>
          </label>

          <label className="flex items-center gap-3">
            <Checkbox
              checked={maintenanceMode}
              onCheckedChange={(v) => setMaintenanceMode(v as boolean)}
            />
            <div className="flex flex-col">
              <span className="text-sm text-foreground">维护模式</span>
              <span className="text-xs text-muted-foreground">启用后平台将显示维护页面，阻止用户访问</span>
            </div>
          </label>

          <p className="text-xs text-muted-foreground">
            功能开关待后端实现，当前仅 UI 演示
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

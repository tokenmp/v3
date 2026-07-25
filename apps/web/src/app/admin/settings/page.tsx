'use client';

import { useState } from 'react';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-4">
      <span className="text-sm text-muted-foreground sm:w-40 shrink-0">{label}</span>
      <span className="text-sm text-foreground">{value}</span>
    </div>
  );
}

const services = [
  { name: 'Auth', status: '运行中' },
  { name: 'Billing', status: '运行中' },
  { name: 'Logging', status: '运行中' },
  { name: 'Notice', status: '运行中' },
  { name: 'Executor', status: '运行中' },
  { name: 'Config', status: '运行中' },
] as const;

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
                <TableHead>服务</TableHead>
                <TableHead>状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {services.map((s) => (
                <TableRow key={s.name}>
                  <TableCell className="font-medium">{s.name}</TableCell>
                  <TableCell>
                    <Badge variant="success">{s.status}</Badge>
                  </TableCell>
                </TableRow>
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

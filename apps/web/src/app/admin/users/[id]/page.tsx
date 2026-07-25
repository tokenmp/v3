'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { adminApi } from '@/lib/api/admin';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ArrowLeft } from 'lucide-react';
import type { ApiKey, UserPlan, RequestLog } from '@/types';

function formatTime(iso: string | null) {
  if (!iso) return '—';
  return new Date(iso).toLocaleString('zh-CN');
}

function formatDate(iso: string | null) {
  if (!iso) return '—';
  return new Date(iso).toLocaleDateString('zh-CN');
}

function apiKeyStatusVariant(status: string) {
  return status === 'active' ? ('success' as const) : ('destructive' as const);
}

function userPlanStatusVariant(status: string) {
  if (status === 'active') return 'success' as const;
  if (status === 'expired') return 'warning' as const;
  return 'destructive' as const;
}

function userPlanStatusLabel(status: string) {
  if (status === 'active') return '生效中';
  if (status === 'expired') return '已过期';
  return '已禁用';
}

function requestStatusVariant(status: string) {
  return status === 'success' ? ('success' as const) : ('destructive' as const);
}

export default function AdminUserDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;

  const { data: user, isLoading } = useQuery({
    queryKey: ['admin', 'user', id],
    queryFn: () => adminApi.getUser(id),
    enabled: !!id,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
      </div>
    );
  }

  if (!user) {
    return (
      <div className="space-y-4">
        <Link href="/admin/users" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" />
          返回用户列表
        </Link>
        <p className="text-center text-muted-foreground py-20">用户不存在</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Back link */}
      <Link href="/admin/users" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" />
        返回用户列表
      </Link>

      {/* User info card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">用户信息</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-sm">
            <div>
              <span className="text-muted-foreground">邮箱</span>
              <p className="font-medium mt-0.5">{user.email}</p>
            </div>
            <div>
              <span className="text-muted-foreground">角色</span>
              <p className="mt-0.5">
                <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                  {user.role === 'admin' ? '管理员' : '用户'}
                </Badge>
              </p>
            </div>
            <div>
              <span className="text-muted-foreground">状态</span>
              <p className="mt-0.5">
                <Badge variant={user.status === 'active' ? 'success' : 'destructive'}>
                  {user.status === 'active' ? '正常' : '已禁用'}
                </Badge>
              </p>
            </div>
            <div>
              <span className="text-muted-foreground">注册时间</span>
              <p className="font-medium mt-0.5">{formatTime(user.createdAt)}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* API Keys card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">API 密钥</CardTitle>
        </CardHeader>
        <CardContent>
          {user.apiKeys.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无 API 密钥</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>密钥</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>创建时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.apiKeys.map((key: ApiKey) => (
                  <TableRow key={key.id}>
                    <TableCell className="text-sm">{key.name}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {key.keyPrefix}…{key.keySuffix}
                    </TableCell>
                    <TableCell>
                      <Badge variant={apiKeyStatusVariant(key.status)}>
                        {key.status === 'active' ? '正常' : '已禁用'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatTime(key.createdAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* User Plans card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">套餐</CardTitle>
        </CardHeader>
        <CardContent>
          {user.userPlans.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无套餐</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>类型</TableHead>
                  <TableHead>总额度</TableHead>
                  <TableHead>剩余额度</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>到期</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.userPlans.map((plan: UserPlan) => (
                  <TableRow key={plan.id}>
                    <TableCell className="text-sm">{plan.planType}</TableCell>
                    <TableCell className="text-sm">{Number(plan.totalQuota).toLocaleString()}</TableCell>
                    <TableCell className="text-sm">{Number(plan.remainingQuota).toLocaleString()}</TableCell>
                    <TableCell>
                      <Badge variant={userPlanStatusVariant(plan.status)}>
                        {userPlanStatusLabel(plan.status)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDate(plan.expiresAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Recent Requests card */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">最近请求</CardTitle>
        </CardHeader>
        <CardContent>
          {user.recentRequests.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4">暂无请求记录</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>模型</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>耗时</TableHead>
                  <TableHead>时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {user.recentRequests.map((req: RequestLog) => (
                  <TableRow key={req.requestId}>
                    <TableCell className="text-sm">{req.model}</TableCell>
                    <TableCell>
                      <Badge variant={requestStatusVariant(req.status)}>
                        {req.status === 'success' ? '成功' : '失败'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm">
                      {req.durationMs != null ? `${req.durationMs}ms` : '—'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatTime(req.createdAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Stats row */}
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        总请求数：<span className="font-medium text-foreground">{user.totalRequests.toLocaleString()}</span>
      </div>
    </div>
  );
}

'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Megaphone,
  Bell,
  GitCommitHorizontal,
  Boxes,
  Sparkles,
  KeyRound,
  Laptop,
  LogOut,
  ChevronRight,
  ShieldCheck,
} from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { toast } from 'sonner';
import { useAuthStore } from '@/lib/auth';
import { userApi } from '@/lib/api/user';
import { noticeApi } from '@/lib/api/notice';
import { authApi } from '@/lib/api/auth';
import { Card, CardContent } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import { ChangePasswordDialog } from '@/components/change-password-dialog';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { cn } from '@/lib/utils';

/**
 * Mobile "Me" / settings hub.
 *
 * On mobile the bottom nav's 4th tab is this page rather than a "更多" sheet.
 * It groups account info, notice entries (announcements / notifications /
 * changelogs), model config, and account actions so the main tabs stay
 * uncluttered. The header bell is hidden on mobile (notices live here).
 */
export default function PanelSettingsPage() {
  const router = useRouter();
  const qc = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const refreshToken = useAuthStore((s) => s.refreshToken);
  const logout = useAuthStore((s) => s.logout);

  const { data: settings } = useQuery({
    queryKey: ['user-settings'] as const,
    queryFn: userApi.getSettings,
  });
  const { data: unread = 0 } = useQuery({
    queryKey: ['notice', 'unread-count'] as const,
    queryFn: noticeApi.unreadCount,
  });

  const [pwOpen, setPwOpen] = useState(false);
  const [logoutOpen, setLogoutOpen] = useState(false);
  const [logoutAllOpen, setLogoutAllOpen] = useState(false);

  const updateSettings = useMutation({
    mutationFn: userApi.updateSettings,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['user-settings'] });
      toast.success('设置已保存');
    },
    onError: () => toast.error('保存失败'),
  });

  const logoutMutation = useMutation({
    mutationFn: () => (refreshToken ? authApi.logout(refreshToken) : Promise.resolve()),
    onSuccess: () => { logout(); router.push('/login'); },
    onError: () => { logout(); router.push('/login'); },
  });
  const logoutAllMutation = useMutation({
    mutationFn: authApi.logoutAll,
    onSuccess: () => { logout(); router.push('/login'); },
    onError: () => toast.error('操作失败'),
  });

  const email = user?.email ?? '';
  const initial = email.charAt(0).toUpperCase() || '?';
  const isAdmin = user?.role === 'admin';
  const createdAt = user?.created_at
    ? new Date(user.created_at).toLocaleString('zh-CN', {
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
    })
    : null;

  const noticeEntries = [
    { href: '/panel/announcements', icon: Megaphone, label: '公告' },
    { href: '/panel/notifications', icon: Bell, label: '通知', badge: unread },
    { href: '/panel/changelogs', icon: GitCommitHorizontal, label: '版本日志' },
  ];
  const configEntries = [
    { href: '/panel/models', icon: Boxes, label: '模型列表' },
    { href: '/panel/auto-model', icon: Sparkles, label: 'Auto 模型' },
    { href: '/panel/keys', icon: KeyRound, label: 'API 密钥' },
  ];

  return (
    <div className="space-y-5">
      {/* Account header */}
      <Card>
        <CardContent className="flex items-center gap-4 p-5">
          <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xl font-semibold">
            {initial}
          </div>
          <div className="min-w-0 flex-1">
            <div className="font-semibold truncate">{email || '用户'}</div>
            <div className="mt-0.5 text-xs text-muted-foreground">
              {isAdmin ? '管理员' : '普通用户'}{createdAt ? ` · 注册于 ${createdAt}` : ''}
            </div>
          </div>
          {isAdmin && (
            <Link
              href="/admin"
              className="flex items-center gap-1 rounded-md bg-primary/10 px-3 py-1.5 text-xs font-medium text-primary"
            >
              <ShieldCheck className="h-3.5 w-3.5" />
              后台
            </Link>
          )}
        </CardContent>
      </Card>

      {/* Preferences */}
      <Section title="偏好设置">
        <Row label="计费方式">
          <div className="flex gap-1">
            {(['coding', 'token'] as const).map((b) => (
              <button
                key={b}
                type="button"
                onClick={() => updateSettings.mutate({ preferredBilling: b })}
                className={cn(
                  'rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                  (settings?.preferredBilling ?? 'coding') === b
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground',
                )}
              >
                {b === 'coding' ? '编程' : 'Token'}
              </button>
            ))}
          </div>
        </Row>
        <Row label="降级开关">
          <Switch
            checked={settings?.fallbackEnabled ?? false}
            onChange={(v) => updateSettings.mutate({ fallbackEnabled: v })}
          />
        </Row>
      </Section>

      {/* Notice entries */}
      <Section title="通知与公告">
        {noticeEntries.map((e) => (
          <LinkRow key={e.href} {...e} />
        ))}
      </Section>

      {/* Config entries */}
      <Section title="模型与密钥">
        {configEntries.map((e) => (
          <LinkRow key={e.href} {...e} />
        ))}
      </Section>

      {/* Account actions */}
      <Section title="账号">
        <button
          type="button"
          onClick={() => setPwOpen(true)}
          className="flex w-full items-center gap-3 px-4 py-3.5 text-sm hover:bg-accent transition-colors"
        >
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          修改密码
        </button>
        <button
          type="button"
          onClick={() => setLogoutAllOpen(true)}
          className="flex w-full items-center gap-3 px-4 py-3.5 text-sm hover:bg-accent transition-colors"
        >
          <Laptop className="h-4 w-4 text-muted-foreground" />
          登出所有设备
        </button>
        <button
          type="button"
          onClick={() => setLogoutOpen(true)}
          className="flex w-full items-center gap-3 px-4 py-3.5 text-sm text-destructive hover:bg-destructive/10 transition-colors"
        >
          <LogOut className="h-4 w-4" />
          退出登录
        </button>
      </Section>

      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
      <ConfirmDialog
        open={logoutOpen} onOpenChange={setLogoutOpen}
        title="退出登录" description="确定要退出当前账户吗？退出后需要重新登录。"
        confirmText="退出登录" destructive
        loading={logoutMutation.isPending}
        onConfirm={() => logoutMutation.mutate()}
      />
      <ConfirmDialog
        open={logoutAllOpen} onOpenChange={setLogoutAllOpen}
        title="登出所有设备" description="此操作将撤销所有设备的登录会话，包括当前设备。你将需要重新登录。确定继续吗？"
        confirmText="确认登出" destructive
        loading={logoutAllMutation.isPending}
        onConfirm={() => logoutAllMutation.mutate()}
      />
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 px-1 text-xs font-medium text-muted-foreground">{title}</div>
      <Card>
        <CardContent className="divide-y p-0">{children}</CardContent>
      </Card>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between px-4 py-3.5">
      <span className="text-sm">{label}</span>
      {children}
    </div>
  );
}

function LinkRow({
  href, icon: Icon, label, badge,
}: { href: string; icon: React.ComponentType<{ className?: string }>; label: string; badge?: number }) {
  return (
    <Link
      href={href}
      className="flex items-center gap-3 px-4 py-3.5 text-sm hover:bg-accent transition-colors"
    >
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="flex-1 text-left">{label}</span>
      {badge != null && badge > 0 && (
        <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-destructive px-1.5 text-[10px] font-semibold text-destructive-foreground">
          {badge > 99 ? '99+' : badge}
        </span>
      )}
      <ChevronRight className="h-4 w-4 text-muted-foreground" />
    </Link>
  );
}

'use client';

import { useState } from 'react';
import { useRouter, usePathname } from 'next/navigation';
import { useMutation } from '@tanstack/react-query';
import { Sun, Moon, ChevronDown, KeyRound, Laptop, LogOut, ChevronRight, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';
import { useTheme } from '@/components/theme-provider';
import { NotificationCenter } from '@/components/notification-center';
import { useAuthStore } from '@/lib/auth';
import { authApi } from '@/lib/api/auth';
import { navGroups } from '@/lib/nav';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';
import { ChangePasswordDialog } from '@/components/change-password-dialog';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { cn } from '@/lib/utils';

interface HeaderProps {
  title?: string;
}

/** Resolve the current page label from the nav config based on the pathname. */
function useCurrentLabel(pathname: string): string {
  for (const group of navGroups) {
    for (const item of group.items) {
      // Exact match for the overview route; prefix match for the rest so
      // nested paths still surface their top-level section label.
      if (item.href === pathname) return item.label;
      if (item.href !== '/panel' && pathname.startsWith(item.href + '/')) return item.label;
    }
  }
  return '';
}

export function Header({ title: _title = 'TokenMP' }: HeaderProps) {
  const { theme, toggleTheme } = useTheme();
  const router = useRouter();
  const pathname = usePathname();
  const user = useAuthStore((s) => s.user);
  const refreshToken = useAuthStore((s) => s.refreshToken);
  const logout = useAuthStore((s) => s.logout);

  const [pwOpen, setPwOpen] = useState(false);
  const [logoutOpen, setLogoutOpen] = useState(false);
  const [logoutAllOpen, setLogoutAllOpen] = useState(false);

  const email = user?.email ?? '';
  const initial = email.charAt(0).toUpperCase() || '?';
  const currentLabel = useCurrentLabel(pathname);

  const logoutMutation = useMutation({
    mutationFn: () => (refreshToken ? authApi.logout(refreshToken) : Promise.resolve()),
    onSuccess: () => {
      logout();
      router.push('/login');
    },
    onError: () => {
      // still log out locally
      logout();
      router.push('/login');
    },
  });

  const logoutAllMutation = useMutation({
    mutationFn: authApi.logoutAll,
    onSuccess: () => {
      logout();
      router.push('/login');
    },
    onError: () => toast.error('操作失败'),
  });

  return (
    <header className="flex h-16 items-center justify-between border-b bg-card px-4 sm:px-6">
      {/* Left: logo (mobile) + breadcrumb */}
      <div className="flex items-center gap-2 min-w-0">
        <div className="md:hidden">
          <TokenMPLogoMark className="h-7 w-7" />
        </div>
        <nav aria-label="面包屑" className="flex items-center gap-1.5 text-sm min-w-0">
          <span className="hidden sm:inline font-semibold text-foreground/80">TokenMP</span>
          <ChevronRight className="hidden sm:block h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <span className="font-semibold text-foreground truncate" title={currentLabel}>
            {currentLabel || 'TokenMP'}
          </span>
        </nav>
      </div>

      {/* Right */}
      <div className="flex items-center gap-2">
        {/* Notifications & announcements (desktop only — on mobile these
            live in /panel/settings) */}
        <div className="hidden md:block">
          <NotificationCenter />
        </div>

        {/* Theme toggle */}
        <button
          type="button"
          onClick={toggleTheme}
          className="focus-inset inline-flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
          aria-label="切换主题"
        >
          <Sun
            className={cn(
              'h-4 w-4 transition-transform duration-300 absolute',
              theme === 'dark' ? 'rotate-90 scale-0' : 'rotate-0 scale-100',
            )}
          />
          <Moon
            className={cn(
              'h-4 w-4 transition-transform duration-300',
              theme === 'dark' ? 'rotate-0 scale-100' : '-rotate-90 scale-0',
            )}
          />
        </button>

        {/* User menu */}
        <DropdownMenu>
          <DropdownMenuTrigger className="focus-inset flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors">
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-semibold">
              {initial}
            </div>
            <span className="hidden sm:inline truncate max-w-[160px]">{email}</span>
            <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuLabel>{email}</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setPwOpen(true)}>
              <KeyRound className="h-4 w-4" />
              修改密码
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setLogoutAllOpen(true)}>
              <Laptop className="h-4 w-4" />
              登出所有设备
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {user?.role === 'admin' && (
              <DropdownMenuItem onClick={() => router.push('/admin')}>
                <ShieldCheck className="h-4 w-4" />
                管理后台
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
              onClick={() => setLogoutOpen(true)}
            >
              <LogOut className="h-4 w-4" />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* Change password dialog */}
      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />

      {/* Logout confirm */}
      <ConfirmDialog
        open={logoutOpen}
        onOpenChange={setLogoutOpen}
        title="退出登录"
        description="确定要退出当前账户吗？退出后需要重新登录。"
        confirmText="退出登录"
        destructive
        loading={logoutMutation.isPending}
        onConfirm={() => logoutMutation.mutate()}
      />

      {/* Logout all confirm */}
      <ConfirmDialog
        open={logoutAllOpen}
        onOpenChange={setLogoutAllOpen}
        title="登出所有设备"
        description="此操作将撤销所有设备的登录会话，包括当前设备。你将需要重新登录。确定继续吗？"
        confirmText="确认登出"
        destructive
        loading={logoutAllMutation.isPending}
        onConfirm={() => logoutAllMutation.mutate()}
      />
    </header>
  );
}

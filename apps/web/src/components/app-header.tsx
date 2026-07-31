'use client';

import { useState, type ReactNode } from 'react';
import { useRouter, usePathname } from 'next/navigation';
import { useMutation } from '@tanstack/react-query';
import { Sun, Moon, ChevronDown, KeyRound, Laptop, LogOut, ChevronRight } from 'lucide-react';
import { toast } from 'sonner';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';
import { useTheme } from '@/components/theme-provider';
import { useAuthStore } from '@/lib/auth';
import { authApi } from '@/lib/api/auth';
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

export interface AppHeaderProps {
  /** Root breadcrumb label (e.g. "TokenMP" or "Admin") */
  breadcrumbRoot: string;
  /** Resolve the current page label from pathname. */
  findLabel: (pathname: string) => string;
  /** Extra actions rendered between the right-side area (before theme toggle). */
  extraActions?: ReactNode;
  /** Extra dropdown menu items rendered before the logout separator. */
  extraMenuItems?: ReactNode;
}

export function AppHeader({
  breadcrumbRoot,
  findLabel,
  extraActions,
  extraMenuItems,
}: AppHeaderProps) {
  const { theme, toggleTheme } = useTheme();
  const router = useRouter();
  const pathname = usePathname();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);

  const [pwOpen, setPwOpen] = useState(false);
  const [logoutOpen, setLogoutOpen] = useState(false);
  const [logoutAllOpen, setLogoutAllOpen] = useState(false);

  const email = user?.email ?? '';
  const initial = email.charAt(0).toUpperCase() || '?';
  const currentLabel = findLabel(pathname);

  const logoutMutation = useMutation({
    mutationFn: authApi.logout,
    onSuccess: () => {
      logout();
      router.push('/login');
    },
    onError: () => {
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
          <span className="hidden sm:inline font-semibold text-foreground/80">{breadcrumbRoot}</span>
          <ChevronRight className="hidden sm:block h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <span className="font-semibold text-foreground truncate" title={currentLabel}>
            {currentLabel || breadcrumbRoot}
          </span>
        </nav>
      </div>

      {/* Right */}
      <div className="flex items-center gap-2">
        {extraActions}

        {/* Theme toggle */}
        <button
          type="button"
          onClick={toggleTheme}
          className="focus-inset inline-flex h-9 w-9 items-center justify-center rounded-md justify-center text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
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
            {extraMenuItems}
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

      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
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

import {
  ShieldCheck,
  Users,
  Key,
  Package,
  UserCheck,
  ScrollText,
  BarChart3,
  Megaphone,
  GitCommitHorizontal,
  Bell,
  Server,
  Box,
  Route as RouteIcon,
  KeyRound,
  Settings,
  type LucideIcon,
} from 'lucide-react';

export interface AdminNavItem {
  label: string;
  href: string;
  icon: LucideIcon;
}

export interface AdminNavGroup {
  label?: string;
  items: AdminNavItem[];
}

/** Admin sidebar navigation. Matches docs/plans/admin-app.md section 3. */
export const adminNavGroups: AdminNavGroup[] = [
  {
    items: [{ label: '控制台', href: '/admin', icon: ShieldCheck }],
  },
  {
    label: '用户域',
    items: [
      { label: '用户管理', href: '/admin/users', icon: Users },
      { label: 'API 密钥', href: '/admin/api-keys', icon: Key },
      { label: '套餐', href: '/admin/plans', icon: Package },
      { label: '用户套餐', href: '/admin/user-plans', icon: UserCheck },
    ],
  },
  {
    label: '运营',
    items: [
      { label: '请求日志', href: '/admin/request-logs', icon: ScrollText },
      { label: '用量统计', href: '/admin/billing/usage', icon: BarChart3 },
    ],
  },
  {
    label: '内容',
    items: [
      { label: '公告', href: '/admin/announcements', icon: Megaphone },
      { label: '版本日志', href: '/admin/changelogs', icon: GitCommitHorizontal },
      { label: '通知', href: '/admin/notifications', icon: Bell },
    ],
  },
  {
    label: '执行',
    items: [
      { label: 'Provider', href: '/admin/providers', icon: Server },
      { label: '上游账号', href: '/admin/credentials', icon: KeyRound },
      { label: '模型配置', href: '/admin/models', icon: Box },
      { label: '路由配置', href: '/admin/routes', icon: RouteIcon },
    ],
  },
  {
    label: '系统',
    items: [{ label: '系统设置', href: '/admin/settings', icon: Settings }],
  },
];

/** Reverse-lookup the current page label from pathname for breadcrumb. */
export function findAdminLabel(pathname: string): string | null {
  for (const g of adminNavGroups) {
    for (const item of g.items) {
      if (item.href === '/admin') {
        if (pathname === '/admin') return item.label;
        continue;
      }
      if (pathname.startsWith(item.href)) return item.label;
    }
  }
  return null;
}

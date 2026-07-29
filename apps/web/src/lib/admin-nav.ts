import {
  ShieldCheck,
  Users,
  Key,
  Package,
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
  RefreshCw,
  Sparkles,
  MoreHorizontal,
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
      { label: 'Auto 模型', href: '/admin/auto-model', icon: Sparkles },
      { label: '路由配置', href: '/admin/routes', icon: RouteIcon },
      { label: '重试策略', href: '/admin/retry', icon: RefreshCw },
    ],
  },
  {
    label: '系统',
    items: [{ label: '系统设置', href: '/admin/settings', icon: Settings }],
  },
];

/** Bottom tab bar items (mobile). Mirrors the panel's mobile pattern: a
 * small set of primary destinations plus a "更多" hub that hosts every
 * other section as grouped link rows (like /panel/settings).
 *
 *   概览 / 用户 / 日志 / 执行 / 更多
 *
 * The desktop sidebar still lists every section; this only affects mobile. */
export const adminMobileTabs: AdminNavItem[] = [
  { label: '概览', href: '/admin', icon: ShieldCheck },
  { label: '用户', href: '/admin/users', icon: Users },
  { label: '日志', href: '/admin/request-logs', icon: ScrollText },
  { label: '执行', href: '/admin/models', icon: Box },
  { label: '更多', href: '/admin/more', icon: MoreHorizontal },
];

/** Sections rendered on the /admin/more mobile hub. Each group becomes a
 * card of link rows (same Section/LinkRow pattern as /panel/settings). The
 * items already surfaced as bottom tabs are intentionally omitted here so
 * the hub stays deduplicated. */
export const adminMobileMoreGroups: { label: string; items: AdminNavItem[] }[] = [
  {
    label: '用户域',
    items: [
      { label: 'API 密钥', href: '/admin/api-keys', icon: Key },
      { label: '套餐', href: '/admin/plans', icon: Package },
    ],
  },
  {
    label: '运营',
    items: [
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
      { label: 'Auto 模型', href: '/admin/auto-model', icon: Sparkles },
      { label: '路由配置', href: '/admin/routes', icon: RouteIcon },
      { label: '重试策略', href: '/admin/retry', icon: RefreshCw },
    ],
  },
  {
    label: '系统',
    items: [
      { label: '系统设置', href: '/admin/settings', icon: Settings },
    ],
  },
];

/** Reverse-lookup the current page label from pathname for breadcrumb. */
export function findAdminLabel(pathname: string): string | null {
  // The /admin/more hub is a mobile-only entry; map it to a friendly label.
  if (pathname === '/admin/more') return '更多';
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

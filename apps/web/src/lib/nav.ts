import {
  LayoutDashboard,
  Key,
  Activity,
  Megaphone,
  Bell,
  GitCommitHorizontal,
  Boxes,
  Sparkles,
  Settings,
  type LucideIcon,
} from 'lucide-react';

export interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
}

export interface NavGroup {
  label?: string;
  items: NavItem[];
}

export const navGroups: NavGroup[] = [
  {
    items: [
      { label: '概览', href: '/panel', icon: LayoutDashboard },
      { label: 'API 密钥', href: '/panel/keys', icon: Key },
      { label: '请求日志', href: '/panel/requests', icon: Activity },
      { label: '模型', href: '/panel/models', icon: Boxes },
      { label: 'Auto 模型', href: '/panel/auto-model', icon: Sparkles },
    ],
  },
  {
    label: '其他',
    items: [
      { label: '公告', href: '/panel/announcements', icon: Megaphone },
      { label: '通知', href: '/panel/notifications', icon: Bell },
      { label: '版本日志', href: '/panel/changelogs', icon: GitCommitHorizontal },
    ],
  },
];

/** Bottom tab bar items (mobile). The 4th tab is the "Me"/settings hub so
 * notice entries (announcements/notifications/changelogs) live inside it
 * rather than crowding the tab bar. */
export const mobileTabs: NavItem[] = [
  { label: '概览', href: '/panel', icon: LayoutDashboard },
  { label: '请求日志', href: '/panel/requests', icon: Activity },
  { label: '模型', href: '/panel/models', icon: Boxes },
  { label: '我的', href: '/panel/settings', icon: Settings },
];

/** Items shown only in the desktop sidebar's "其他" group (not in the mobile
 * tab bar — on mobile they are reached via /panel/settings). */
export const mobileMore: NavItem[] = navGroups[1]!.items;

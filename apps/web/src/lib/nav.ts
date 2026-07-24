import {
  LayoutDashboard,
  Key,
  Activity,
  Megaphone,
  Bell,
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
    ],
  },
  {
    label: '其他',
    items: [
      { label: '公告', href: '/panel/announcements', icon: Megaphone },
      { label: '通知', href: '/panel/notifications', icon: Bell },
    ],
  },
];

/** Bottom tab bar items (first 3). */
export const mobileTabs: NavItem[] = navGroups[0]!.items.slice(0, 3);

/** "More" sheet items (remaining). */
export const mobileMore: NavItem[] = [
  ...navGroups[0]!.items.slice(3),
  ...navGroups[1]!.items,
];

'use client';

import Link from 'next/link';
import type { ReactNode } from 'react';
import { TokenMPLogoMark } from '@/components/tokenmp-logo';

export interface AuthShellProps {
  title: string;
  desc?: string;
  mode: 'login' | 'register' | 'forgot';
  children: ReactNode;
  footer?: ReactNode;
}

const TABS = [
  { key: 'login' as const, label: '登录', href: '/login' },
  { key: 'register' as const, label: '注册', href: '/register' },
];

export function AuthShell({ title, desc, mode, children, footer }: AuthShellProps) {
  const showTabs = mode !== 'forgot';

  return (
    <div className="grid min-h-dvh lg:grid-cols-2">
      {/* Decorative blobs */}
      <div
        className="pointer-events-none absolute top-[-10%] left-[-5%] h-[500px] w-[500px] rounded-full opacity-30"
        style={{
          background:
            'radial-gradient(circle, color-mix(in oklch, var(--primary) 45%, transparent) 0%, transparent 70%)',
        }}
      />
      <div
        className="pointer-events-none absolute right-[-8%] bottom-[-8%] h-[420px] w-[420px] rounded-full opacity-25"
        style={{
          background:
            'radial-gradient(circle, color-mix(in oklch, var(--secondary) 50%, transparent) 0%, transparent 70%)',
        }}
      />

      {/* Left brand panel */}
      <div className="hidden lg:flex relative flex-col justify-between p-10 xl:p-16 overflow-hidden">
        <div className="flex items-center gap-2.5">
          <TokenMPLogoMark className="h-8 w-8" />
          <span className="text-xl font-bold tracking-tight">TokenMP</span>
        </div>

        <div className="flex flex-col gap-6">
          <h1 className="text-4xl font-bold leading-snug tracking-tight xl:text-5xl">
            统一接入主流 AI 模型的
            <br />
            API 网关
          </h1>
          <p className="max-w-md text-base leading-relaxed text-muted-foreground">
            登录后管理 API Key、模型路由、请求日志与套餐用量。
          </p>
        </div>

        <div className="flex items-center gap-3 rounded-2xl border bg-background/60 p-5 backdrop-blur-sm">
          <TokenMPLogoMark className="h-10 w-10" />
          <div>
            <p className="font-semibold">TokenMP</p>
            <p className="text-sm text-muted-foreground">
              安全 · 可靠 · 高性能的 AI 模型网关
            </p>
          </div>
        </div>
      </div>

      {/* Right form panel */}
      <div className="relative flex min-h-dvh flex-col justify-start px-6 py-10 sm:px-8 lg:items-center lg:justify-center">
        {/* Mobile logo */}
        <div className="flex items-center gap-2.5 lg:hidden mb-8">
          <TokenMPLogoMark className="h-7 w-7" />
          <span className="text-lg font-bold tracking-tight">TokenMP</span>
        </div>

        <div className="w-full max-w-md">
          {/* Tabs */}
          {showTabs && (
            <div className="mb-6 flex gap-1 rounded-xl bg-muted p-1">
              {TABS.map((tab) => (
                <Link
                  key={tab.key}
                  href={tab.href}
                  className={`flex-1 rounded-lg py-2 text-center text-sm font-medium transition-colors ${
                    mode === tab.key
                      ? 'bg-background shadow-sm text-foreground'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {tab.label}
                </Link>
              ))}
            </div>
          )}

          {/* Card */}
          <div className="rounded-[2rem] border bg-background/70 shadow-auth-card p-8 sm:p-10">
            <h2 className="text-2xl font-bold tracking-tight">{title}</h2>
            {desc && (
              <p className="mt-1.5 text-sm text-muted-foreground">{desc}</p>
            )}

            <div className="mt-6">{children}</div>

            {footer && <div className="mt-6">{footer}</div>}
          </div>
        </div>
      </div>
    </div>
  );
}

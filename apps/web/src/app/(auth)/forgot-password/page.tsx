'use client';

import Link from 'next/link';
import { AuthShell } from '@/components/auth-shell';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export default function ForgotPasswordPage() {
  return (
    <AuthShell
      title="忘记密码"
      desc="重置你的 TokenMP 账号密码"
      mode="forgot"
      footer={
        <p className="text-center text-sm text-muted-foreground">
          <Link
            href="/login"
            className="font-medium text-primary underline-offset-4 hover:underline"
          >
            返回登录
          </Link>
        </p>
      }
    >
      <div className="flex flex-col gap-4">
        {/* Disabled email field */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="email">邮箱</Label>
          <Input
            id="email"
            type="email"
            placeholder="you@example.com"
            disabled
          />
        </div>

        {/* Placeholder notice card */}
        <div className="rounded-xl border bg-muted/50 p-4">
          <p className="text-sm leading-relaxed text-muted-foreground">
            密码重置功能开发中，如需重置密码请联系管理员。
          </p>
        </div>
      </div>
    </AuthShell>
  );
}

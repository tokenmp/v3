'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { Eye, EyeOff } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authApi } from '@/lib/api/auth';
import { registerSchema, passwordStrength, type RegisterValues } from '@/lib/validators';
import { ApiError } from '@/lib/api-error';

function StrengthBar({ password }: { password: string }) {
  const score = passwordStrength(password);
  // score 0 = no input → all grey; score 1-2 = red; 3 = orange; 4 = blue; 5 = green
  // But passwordStrength returns 0..4, so map: 0→grey, 1→red, 2→orange, 3→blue, 4→green
  const colorMap: Record<number, string> = {
    0: 'bg-muted-foreground/30',
    1: 'bg-destructive',
    2: 'bg-orange-500',
    3: 'bg-blue-500',
    4: 'bg-green-500',
  };
  const labelMap: Record<number, string> = {
    0: '',
    1: '弱',
    2: '一般',
    3: '较强',
    4: '强',
  };

  return (
    <div className="flex items-center gap-2">
      <div className="flex flex-1 gap-1">
        {Array.from({ length: 4 }).map((_, i) => (
          <div
            key={i}
            className={`h-1.5 flex-1 rounded-full transition-colors ${
              i < score ? colorMap[score] : 'bg-muted-foreground/20'
            }`}
          />
        ))}
      </div>
      {score > 0 && (
        <span className="text-xs text-muted-foreground">{labelMap[score]}</span>
      )}
    </div>
  );
}

export default function RegisterPage() {
  const router = useRouter();

  const [showPassword, setShowPassword] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [loading, setLoading] = useState(false);

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors },
  } = useForm<RegisterValues>({
    resolver: zodResolver(registerSchema),
    defaultValues: { email: '', password: '', confirmPassword: '' },
  });

  const password = watch('password');

  const onSubmit = async (values: RegisterValues) => {
    setLoading(true);
    try {
      await authApi.register({ email: values.email, password: values.password });
      toast.success('注册成功，请登录');
      router.push('/login');
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        toast.error(err.message);
      } else if (err instanceof Error) {
        toast.error(err.message);
      } else {
        toast.error('注册失败，请稍后重试');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <AuthShell
      title="注册"
      desc="创建你的 TokenMP 账号"
      mode="register"
      footer={
        <p className="text-center text-sm text-muted-foreground">
          已有账号？{' '}
          <Link
            href="/login"
            className="font-medium text-primary underline-offset-4 hover:underline"
          >
            立即登录
          </Link>
        </p>
      }
    >
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        {/* Email */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="email">邮箱</Label>
          <Input
            id="email"
            type="email"
            placeholder="you@example.com"
            autoComplete="email"
            {...register('email')}
          />
          {errors.email && (
            <p className="text-xs text-destructive">{errors.email.message}</p>
          )}
        </div>

        {/* Password */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="password">密码</Label>
          <div className="relative">
            <Input
              id="password"
              type={showPassword ? 'text' : 'password'}
              placeholder="至少 12 位"
              autoComplete="new-password"
              className="pr-10"
              {...register('password')}
            />
            <button
              type="button"
              tabIndex={-1}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setShowPassword((v) => !v)}
              aria-label={showPassword ? '隐藏密码' : '显示密码'}
            >
              {showPassword ? (
                <EyeOff className="h-4 w-4" />
              ) : (
                <Eye className="h-4 w-4" />
              )}
            </button>
          </div>
          <StrengthBar password={password ?? ''} />
          {errors.password && (
            <p className="text-xs text-destructive">{errors.password.message}</p>
          )}
        </div>

        {/* Confirm password */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="confirmPassword">确认密码</Label>
          <div className="relative">
            <Input
              id="confirmPassword"
              type={showConfirm ? 'text' : 'password'}
              placeholder="再次输入密码"
              autoComplete="new-password"
              className="pr-10"
              {...register('confirmPassword')}
            />
            <button
              type="button"
              tabIndex={-1}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setShowConfirm((v) => !v)}
              aria-label={showConfirm ? '隐藏密码' : '显示密码'}
            >
              {showConfirm ? (
                <EyeOff className="h-4 w-4" />
              ) : (
                <Eye className="h-4 w-4" />
              )}
            </button>
          </div>
          {errors.confirmPassword && (
            <p className="text-xs text-destructive">
              {errors.confirmPassword.message}
            </p>
          )}
        </div>

        {/* Submit */}
        <Button
          type="submit"
          variant="default"
          size="lg"
          className="w-full"
          disabled={loading}
        >
          {loading ? '注册中...' : '注册'}
        </Button>
      </form>
    </AuthShell>
  );
}

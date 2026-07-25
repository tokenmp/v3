'use client';

import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation } from '@tanstack/react-query';
import { Eye, EyeOff } from 'lucide-react';
import { toast } from 'sonner';
import {
  Dialog,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authApi } from '@/lib/api/auth';
import {
  changePasswordSchema,
  passwordStrength,
  type ChangePasswordValues,
} from '@/lib/validators';

const STRENGTH_COLORS = [
  'bg-muted',
  'bg-destructive',
  'bg-warning',
  'bg-warning',
  'bg-success',
  'bg-success',
];
const STRENGTH_LABELS = ['', '弱', '一般', '一般', '强', '强'];

interface ChangePasswordDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ChangePasswordDialog({ open, onOpenChange }: ChangePasswordDialogProps) {
  const [showCurrent, setShowCurrent] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);

  const {
    register,
    handleSubmit,
    watch,
    reset,
    formState: { errors },
  } = useForm<ChangePasswordValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { currentPassword: '', newPassword: '', confirmPassword: '' },
  });

  const newPassword = watch('newPassword') ?? '';
  const strength = passwordStrength(newPassword);

  const mutation = useMutation({
    mutationFn: (values: ChangePasswordValues) =>
      authApi.changePassword({
        current_password: values.currentPassword,
        new_password: values.newPassword,
      }),
    onSuccess: () => {
      toast.success('密码修改成功');
      reset();
      onOpenChange(false);
    },
    onError: (err: unknown) => {
      toast.error(err instanceof Error ? err.message : '密码修改失败');
    },
  });

  const handleOpenChange = (v: boolean) => {
    if (!v) reset();
    onOpenChange(v);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogHeader>
        <DialogTitle>修改密码</DialogTitle>
        <DialogDescription>更新你的账户登录密码。</DialogDescription>
      </DialogHeader>
      <form
        onSubmit={handleSubmit((values) => mutation.mutate(values))}
        className="space-y-4"
      >
        {/* Current password */}
        <div>
          <Label htmlFor="cp-current">当前密码</Label>
          <div className="relative mt-1.5">
            <Input
              id="cp-current"
              type={showCurrent ? 'text' : 'password'}
              autoComplete="current-password"
              {...register('currentPassword')}
            />
            <button
              type="button"
              tabIndex={-1}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setShowCurrent((v) => !v)}
              aria-label={showCurrent ? '隐藏密码' : '显示密码'}
            >
              {showCurrent ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
          {errors.currentPassword && (
            <p className="mt-1 text-xs text-destructive">{errors.currentPassword.message}</p>
          )}
        </div>

        {/* New password */}
        <div>
          <Label htmlFor="cp-new">新密码</Label>
          <div className="relative mt-1.5">
            <Input
              id="cp-new"
              type={showNew ? 'text' : 'password'}
              autoComplete="new-password"
              {...register('newPassword')}
            />
            <button
              type="button"
              tabIndex={-1}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setShowNew((v) => !v)}
              aria-label={showNew ? '隐藏密码' : '显示密码'}
            >
              {showNew ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
          {errors.newPassword && (
            <p className="mt-1 text-xs text-destructive">{errors.newPassword.message}</p>
          )}
          {newPassword && (
            <div className="mt-2 space-y-1">
              <div className="flex gap-1">
                {[1, 2, 3, 4].map((i) => (
                  <div
                    key={i}
                    className={`h-1.5 flex-1 rounded-full ${
                      strength >= i ? STRENGTH_COLORS[strength] : 'bg-muted'
                    }`}
                  />
                ))}
              </div>
              <p className="text-xs text-muted-foreground">
                密码强度：{STRENGTH_LABELS[strength]}
              </p>
            </div>
          )}
        </div>

        {/* Confirm password */}
        <div>
          <Label htmlFor="cp-confirm">确认新密码</Label>
          <div className="relative mt-1.5">
            <Input
              id="cp-confirm"
              type={showConfirm ? 'text' : 'password'}
              autoComplete="new-password"
              {...register('confirmPassword')}
            />
            <button
              type="button"
              tabIndex={-1}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setShowConfirm((v) => !v)}
              aria-label={showConfirm ? '隐藏密码' : '显示密码'}
            >
              {showConfirm ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
          {errors.confirmPassword && (
            <p className="mt-1 text-xs text-destructive">{errors.confirmPassword.message}</p>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => handleOpenChange(false)}
            disabled={mutation.isPending}
          >
            取消
          </Button>
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? '提交中...' : '修改密码'}
          </Button>
        </DialogFooter>
      </form>
    </Dialog>
  );
}

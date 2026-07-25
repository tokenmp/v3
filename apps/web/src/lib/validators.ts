import { z } from 'zod';

/** Email + password validators aligned to the auth OpenAPI contract.
 *  Register password: 12..128 chars. Login: non-empty. */

export const emailSchema = z
  .string()
  .min(1, '请输入邮箱')
  .email('邮箱格式不正确')
  .max(255, '邮箱过长');

export const registerPasswordSchema = z
  .string()
  .min(12, '密码至少 12 位')
  .max(128, '密码最多 128 位')
  .refine((v) => !/[\u0000-\u001F]/.test(v), '密码不能包含控制字符');

export const loginPasswordSchema = z.string().min(1, '请输入密码');

export const loginSchema = z.object({
  email: emailSchema,
  password: loginPasswordSchema,
  remember: z.boolean().optional(),
});

export const registerSchema = z
  .object({
    email: emailSchema,
    password: registerPasswordSchema,
    confirmPassword: z.string().min(1, '请确认密码'),
  })
  .refine((d) => d.password === d.confirmPassword, {
    message: '两次密码不一致',
    path: ['confirmPassword'],
  });

export const changePasswordSchema = z
  .object({
    currentPassword: z.string().min(1, '请输入当前密码'),
    newPassword: registerPasswordSchema,
    confirmPassword: z.string().min(1, '请确认新密码'),
  })
  .refine((d) => d.newPassword === d.confirmPassword, {
    message: '两次密码不一致',
    path: ['confirmPassword'],
  })
  .refine((d) => d.currentPassword !== d.newPassword, {
    message: '新密码不能与当前密码相同',
    path: ['newPassword'],
  });

export type LoginValues = z.infer<typeof loginSchema>;
export type RegisterValues = z.infer<typeof registerSchema>;
export type ChangePasswordValues = z.infer<typeof changePasswordSchema>;

/** Estimate password strength 0..4 for the indicator. */
export function passwordStrength(pw: string): number {
  if (!pw) return 0;
  let score = 0;
  if (pw.length >= 8) score++;
  if (pw.length >= 12) score++;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) score++;
  if (/\d/.test(pw)) score++;
  if (/[^\w\s]/.test(pw)) score++;
  return Math.min(4, score);
}
